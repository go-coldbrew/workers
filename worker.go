// Package workers provides a worker lifecycle library for Go, built on
// [thejerf/suture]. It manages background goroutines with automatic panic
// recovery, configurable restart with backoff, and structured shutdown.
//
// # Architecture
//
// Every worker runs inside its own supervisor subtree. This means:
//   - Each worker gets panic recovery and restart independently
//   - Workers can dynamically spawn child workers via [WorkerInfo]
//   - When a parent worker stops, all its children stop (scoped lifecycle)
//   - The supervisor tree prevents cascading failures and CPU-burn restart storms
//
// # Quick Start
//
// Create workers with [NewWorker] and run them with [Run]:
//
//	workers.Run(ctx, []*workers.Worker{
//	    workers.NewWorker("kafka").HandlerFunc(consume),
//	    workers.NewWorker("cleanup").HandlerFunc(cleanup).Every(5 * time.Minute).WithRestart(true),
//	})
//
// # Middleware
//
// Cross-cutting concerns like tracing, logging, and panic recovery are
// implemented as [Middleware]. The middleware chain follows the gRPC
// interceptor convention: a flat function that calls next to continue:
//
//	func myMiddleware(ctx context.Context, info *workers.WorkerInfo, next workers.CycleFunc) error {
//	    // before
//	    err := next(ctx, info)
//	    // after
//	    return err
//	}
//
// Attach middleware per-worker via [Worker.Interceptors] or per-run via
// [WithInterceptors]. Built-in middleware is available in the middleware/
// sub-package.
//
// # Helpers
//
// Common patterns are provided as helpers:
//   - [EveryInterval] — periodic execution on a fixed interval
//   - [ChannelWorker] — consume items from a channel one at a time
//   - [BatchChannelWorker] — collect items into batches, flush on size or timer
//
// # Dynamic Workers
//
// Manager workers can spawn and remove child workers at runtime using
// the Add, Remove, and Children methods on [WorkerInfo].
// Children join the parent's supervisor subtree and get full framework
// guarantees (panic recovery, restart). See [Example_dynamicWorkerPool].
//
// [thejerf/suture]: https://github.com/thejerf/suture
package workers

import (
	"context"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/thejerf/suture/v4"
)

// WorkerInfo carries worker metadata and child management. The framework
// always creates it — it is never nil. context.Context handles
// cancellation/deadlines/values; WorkerInfo handles everything worker-specific.
type WorkerInfo struct {
	name    string
	attempt int

	// child management, set by framework
	sup        *suture.Supervisor
	childrenMu sync.Mutex
	children   map[string]childEntry
	cfg        *runConfig
	active     *atomic.Int32
	metrics    Metrics
}

// childEntry tracks a child worker and its supervisor token.
type childEntry struct {
	token  suture.ServiceToken
	worker *Worker
}

// Name returns the worker's name as passed to [NewWorker].
func (info *WorkerInfo) Name() string { return info.name }

// Attempt returns the restart attempt number (0 on first run).
func (info *WorkerInfo) Attempt() int { return info.attempt }

// NewWorkerInfo creates a [WorkerInfo] with the given name and attempt.
// This is useful for testing middleware — the framework creates fully
// populated instances internally.
func NewWorkerInfo(name string, attempt int) *WorkerInfo {
	return &WorkerInfo{name: name, attempt: attempt}
}

// Add adds or replaces a child worker under this worker's supervisor subtree.
// If a worker with the same name already exists, it is removed first.
// Children inherit run-level interceptors, metrics (unless overridden via
// [Worker.WithMetrics]), and scoped lifecycle — when this worker stops,
// all its children stop too.
func (info *WorkerInfo) Add(w *Worker) {
	if info.sup == nil {
		return
	}
	info.childrenMu.Lock()
	defer info.childrenMu.Unlock()

	if w.metrics == nil {
		w.metrics = info.metrics
	}
	// Remove existing worker with the same name (replace semantics).
	if entry, ok := info.children[w.name]; ok {
		_ = info.sup.Remove(entry.token)
		delete(info.children, w.name)
	}
	tok := addWorkerToSupervisor(info.sup, w, info.cfg, info.active)
	info.children[w.name] = childEntry{token: tok, worker: w}
}

// Remove stops a child worker by name.
func (info *WorkerInfo) Remove(name string) {
	if info.sup == nil {
		return
	}
	info.childrenMu.Lock()
	defer info.childrenMu.Unlock()

	if entry, ok := info.children[name]; ok {
		_ = info.sup.Remove(entry.token)
		delete(info.children, name)
	}
}

// Children returns the names of currently running child workers.
func (info *WorkerInfo) Children() []string {
	info.childrenMu.Lock()
	defer info.childrenMu.Unlock()

	names := make([]string, 0, len(info.children))
	for name := range info.children {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// Child returns a snapshot of a running child worker, or nil if not found.
// The returned copy is safe for inspection but mutations have no effect
// on the running worker.
func (info *WorkerInfo) Child(name string) *Worker {
	info.childrenMu.Lock()
	defer info.childrenMu.Unlock()

	if entry, ok := info.children[name]; ok {
		cp := *entry.worker
		return &cp
	}
	return nil
}

// CycleHandler handles worker execution cycles.
// For periodic workers, RunCycle is called once per tick.
// Close is called once when the worker stops, allowing cleanup of resources.
type CycleHandler interface {
	RunCycle(ctx context.Context, info *WorkerInfo) error
	Close() error
}

// CycleFunc adapts a plain function into a [CycleHandler].
// Close is a no-op — use this for simple, stateless handlers.
type CycleFunc func(ctx context.Context, info *WorkerInfo) error

func (fn CycleFunc) RunCycle(ctx context.Context, info *WorkerInfo) error { return fn(ctx, info) }

// Close is a no-op for CycleFunc.
func (fn CycleFunc) Close() error { return nil }

// Middleware intercepts each execution cycle.
// Call next to continue the chain. Matches gRPC interceptor convention.
type Middleware func(ctx context.Context, info *WorkerInfo, next CycleFunc) error

// Worker represents a background goroutine managed by the framework.
// Create with [NewWorker] and configure with builder methods.
type Worker struct {
	name             string
	handler          CycleHandler
	interceptors     []Middleware
	interval         time.Duration // stored as data, wrapping deferred to startup
	jitterPercent    int           // -1 = inherit run-level default, 0 = no jitter
	initialDelay     time.Duration
	restartOnFail    bool
	failureDecay     float64
	failureThreshold float64
	failureBackoff   time.Duration
	backoffJitter    *suture.Jitter
	timeout          time.Duration
	metrics          Metrics // nil means inherit from parent
}

// NewWorker creates a [Worker] with the given name.
// Set the handler via [Worker.Handler] or [Worker.HandlerFunc].
func NewWorker(name string) *Worker {
	return &Worker{name: name, jitterPercent: -1}
}

// GetName returns the worker's name.
func (w *Worker) GetName() string { return w.name }

// GetHandler returns the worker's [CycleHandler], or nil if not set.
func (w *Worker) GetHandler() CycleHandler { return w.handler }

// Handler sets the worker's [CycleHandler]. Use this for handlers that
// need cleanup via Close (e.g., database connections, leases).
func (w *Worker) Handler(h CycleHandler) *Worker {
	w.handler = h
	return w
}

// HandlerFunc sets the worker's handler from a plain function.
// This is the common case for simple, stateless workers.
func (w *Worker) HandlerFunc(fn CycleFunc) *Worker {
	w.handler = fn
	return w
}

// Every configures the worker to run periodically at the given interval.
// The interval is stored as data — wrapping is deferred to startup where
// jitter configuration is resolved.
func (w *Worker) Every(d time.Duration) *Worker {
	w.interval = d
	return w
}

// WithJitter sets per-worker jitter as a percentage of the base interval.
// Each tick is randomized within ±percent of the base. Requires [Worker.Every].
// Setting WithJitter(0) explicitly disables jitter even when a run-level
// default is set via [WithDefaultJitter].
func (w *Worker) WithJitter(percent int) *Worker {
	w.jitterPercent = percent
	return w
}

// WithInitialDelay delays the first tick to stagger startup. Requires [Worker.Every].
func (w *Worker) WithInitialDelay(d time.Duration) *Worker {
	w.initialDelay = d
	return w
}

// Interceptors replaces the worker-level interceptor list.
func (w *Worker) Interceptors(mw ...Middleware) *Worker {
	w.interceptors = append([]Middleware(nil), mw...)
	return w
}

// AddInterceptors appends to the worker-level interceptor list.
func (w *Worker) AddInterceptors(mw ...Middleware) *Worker {
	w.interceptors = append(w.interceptors, mw...)
	return w
}

// WithRestart configures whether the worker should be restarted on failure.
// When true, the supervisor restarts the worker with backoff on non-context errors.
func (w *Worker) WithRestart(restart bool) *Worker {
	w.restartOnFail = restart
	return w
}

// WithFailureDecay sets the rate at which failure count decays over time.
// A value of 1.0 means failures decay by one per second. Suture default is 1.0.
func (w *Worker) WithFailureDecay(decay float64) *Worker {
	w.failureDecay = decay
	return w
}

// WithFailureThreshold sets the number of failures allowed before the
// supervisor gives up restarting. Suture default is 5.
func (w *Worker) WithFailureThreshold(threshold float64) *Worker {
	w.failureThreshold = threshold
	return w
}

// WithFailureBackoff sets the duration to wait between restarts.
// Suture default is 15 seconds.
func (w *Worker) WithFailureBackoff(d time.Duration) *Worker {
	w.failureBackoff = d
	return w
}

// WithBackoffJitter adds random jitter to the backoff duration to prevent
// thundering herd on coordinated restarts.
func (w *Worker) WithBackoffJitter(jitter suture.Jitter) *Worker {
	w.backoffJitter = &jitter
	return w
}

// WithTimeout sets the maximum time to wait for the worker to stop during
// graceful shutdown. Suture default is 10 seconds.
func (w *Worker) WithTimeout(d time.Duration) *Worker {
	w.timeout = d
	return w
}

// WithMetrics sets a per-worker metrics implementation, overriding the
// metrics inherited from the parent [WorkerInfo] or [Run] options.
func (w *Worker) WithMetrics(m Metrics) *Worker {
	w.metrics = m
	return w
}

// sutureSpec returns a suture.Spec built from the worker's configuration.
// Zero values are omitted so suture uses its defaults.
func (w *Worker) sutureSpec(hook suture.EventHook) suture.Spec {
	spec := suture.Spec{EventHook: hook}
	if w.failureDecay > 0 {
		spec.FailureDecay = w.failureDecay
	}
	if w.failureThreshold > 0 {
		spec.FailureThreshold = w.failureThreshold
	}
	if w.failureBackoff > 0 {
		spec.FailureBackoff = w.failureBackoff
	}
	if w.backoffJitter != nil {
		spec.BackoffJitter = *w.backoffJitter
	}
	if w.timeout > 0 {
		spec.Timeout = w.timeout
	}
	return spec
}
