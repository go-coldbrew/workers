// Package workers provides a worker lifecycle library for Go, built on
// [thejerf/suture]. It manages background goroutines with automatic panic
// recovery, configurable restart with backoff, tracing, and structured shutdown.
//
// # Architecture
//
// Every worker runs inside its own supervisor subtree. This means:
//   - Each worker gets panic recovery and restart independently
//   - Workers can dynamically spawn child workers via [WorkerContext]
//   - When a parent worker stops, all its children stop (scoped lifecycle)
//   - The supervisor tree prevents cascading failures and CPU-burn restart storms
//
// # Quick Start
//
// Create workers with [NewWorker] and run them with [Run]:
//
//	workers.Run(ctx, []*workers.Worker{
//	    workers.NewWorker("kafka", consume),
//	    workers.NewWorker("cleanup", cleanup).Every(5 * time.Minute).WithRestart(true),
//	})
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
// the Add, Remove, and Children methods on [WorkerContext].
// Children join the parent's supervisor subtree and get full framework
// guarantees (tracing, panic recovery, restart). See [Example_dynamicWorkerPool].
//
// [thejerf/suture]: https://github.com/thejerf/suture
package workers

import (
	"context"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/thejerf/suture/v4"
)

// WorkerContext extends context.Context with worker metadata and
// dynamic child worker management. The framework creates these —
// users never need to implement this interface.
type WorkerContext interface {
	context.Context
	// Name returns the worker's name.
	Name() string
	// Attempt returns the restart attempt number (0 on first run).
	Attempt() int
	// Add adds or replaces a child worker by name under the same supervisor.
	// If a worker with the same name already exists, it is removed first.
	// Children get full framework guarantees (tracing, panic recovery, restart).
	Add(w *Worker)
	// Remove stops a child worker by name.
	Remove(name string)
	// Children returns the names of currently running child workers.
	Children() []string
}

// workerContext is the framework-owned implementation of WorkerContext.
type workerContext struct {
	context.Context
	name     string
	attempt  int
	sup      *suture.Supervisor
	children sync.Map // name (string) → suture.ServiceToken
	cfg      *runConfig
	active   *atomic.Int32
}

func (wc *workerContext) Name() string { return wc.name }
func (wc *workerContext) Attempt() int { return wc.attempt }

func (wc *workerContext) Add(w *Worker) {
	if wc.sup == nil {
		return
	}
	// Inherit parent metrics if the child doesn't override.
	if w.metrics == nil {
		w.metrics = wc.cfg.metrics
	}
	// Remove existing worker with the same name (replace semantics).
	if tok, loaded := wc.children.LoadAndDelete(w.name); loaded {
		_ = wc.sup.Remove(tok.(suture.ServiceToken))
	}
	// Each child gets its own supervisor subtree, scoped to this parent.
	tok := addWorkerToSupervisor(wc.sup, w, wc.cfg, wc.active)
	wc.children.Store(w.name, tok)
}

func (wc *workerContext) Remove(name string) {
	if wc.sup == nil {
		return
	}
	if tok, loaded := wc.children.LoadAndDelete(name); loaded {
		_ = wc.sup.Remove(tok.(suture.ServiceToken))
	}
}

func (wc *workerContext) Children() []string {
	var names []string
	wc.children.Range(func(key, _ any) bool {
		names = append(names, key.(string))
		return true
	})
	sort.Strings(names)
	return names
}

func newWorkerContext(ctx context.Context, name string, attempt int, sup *suture.Supervisor, cfg *runConfig, active *atomic.Int32) WorkerContext {
	if cfg == nil {
		cfg = &runConfig{metrics: BaseMetrics{}}
	}
	return &workerContext{Context: ctx, name: name, attempt: attempt, sup: sup, cfg: cfg, active: active}
}

// Worker represents a background goroutine managed by the framework.
// Create with NewWorker and configure with builder methods.
type Worker struct {
	name             string
	run              func(WorkerContext) error
	restartOnFail    bool
	failureDecay     float64
	failureThreshold float64
	failureBackoff   time.Duration
	backoffJitter    *suture.Jitter
	timeout          time.Duration
	metrics          Metrics // nil means inherit from parent
	interval         time.Duration
	jitterPercent    *int
	initialDelay     time.Duration
	middlewares      []Middleware
}

// NewWorker creates a Worker with the given name and run function.
// The run function should block until ctx is cancelled or an error occurs.
func NewWorker(name string, run func(WorkerContext) error) *Worker {
	return &Worker{name: name, run: run}
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
// metrics inherited from the parent WorkerContext or Run options.
func (w *Worker) WithMetrics(m Metrics) *Worker {
	w.metrics = m
	return w
}

// Every configures the worker to run its function periodically at the given interval.
// The original run function is called once per tick. If it returns an error,
// the behavior depends on WithRestart: if true, the ticker worker restarts;
// if false, it exits.
//
// The interval loop is constructed when the worker starts, not when Every is called.
// This allows WithJitter and WithDefaultJitter to influence the loop behavior.
func (w *Worker) Every(d time.Duration) *Worker {
	w.interval = d
	return w
}

// WithJitter sets the jitter percentage for periodic workers (set via Every).
// Each tick's interval is randomized within ±percent of the base interval.
// For example, WithJitter(20) on a 100ms interval yields intervals in [80ms, 120ms).
// Requires Every to have been called; has no effect on non-periodic workers.
// Worker-level jitter takes precedence over run-level WithDefaultJitter.
func (w *Worker) WithJitter(percent int) *Worker {
	w.jitterPercent = &percent
	return w
}

// WithInitialDelay delays the first tick of a periodic worker by the given duration.
// Pairs with WithJitter to stagger workers that share the same interval,
// preventing thundering herd at process startup.
// The delay runs outside the middleware chain — middleware only wraps each tick.
// Has no effect on non-periodic workers.
func (w *Worker) WithInitialDelay(d time.Duration) *Worker {
	w.initialDelay = d
	return w
}

// Use appends middleware to the worker's middleware chain.
// Middleware wraps each execution cycle. For periodic workers (Every),
// middleware runs on every tick, not once for the worker lifetime.
// Worker-level middleware runs inside (closer to the function) run-level
// middleware set via WithMiddleware.
//
// The first middleware in the list is the outermost wrapper (runs first
// on entry, last on exit), matching the convention of gRPC interceptors.
func (w *Worker) Use(mw ...Middleware) *Worker {
	w.middlewares = append(w.middlewares, mw...)
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
