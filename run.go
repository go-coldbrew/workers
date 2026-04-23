package workers

import (
	"context"
	"log/slog"
	"sync/atomic"

	"github.com/thejerf/suture/v4"
)

// RunOption configures the behavior of [Run].
type RunOption func(*runConfig)

type runConfig struct {
	metrics      Metrics
	interceptors []Middleware
	defaultJitter int // -1 = not set
}

// WithMetrics sets the metrics implementation for all workers started by [Run].
// Workers inherit this unless they override via [Worker.WithMetrics].
// If not set, [BaseMetrics] is used.
func WithMetrics(m Metrics) RunOption {
	return func(c *runConfig) {
		if m != nil {
			c.metrics = m
		}
	}
}

// WithInterceptors replaces the run-level interceptor list.
// Run-level interceptors wrap outside worker-level interceptors.
func WithInterceptors(mw ...Middleware) RunOption {
	return func(c *runConfig) {
		c.interceptors = mw
	}
}

// AddInterceptors appends to the run-level interceptor list.
func AddInterceptors(mw ...Middleware) RunOption {
	return func(c *runConfig) {
		c.interceptors = append(c.interceptors, mw...)
	}
}

// WithDefaultJitter sets a run-level default jitter percentage for all
// periodic workers. Worker-level [Worker.WithJitter] takes precedence.
// Setting Worker.WithJitter(0) disables jitter for a specific worker
// even when a run-level default is set.
func WithDefaultJitter(percent int) RunOption {
	return func(c *runConfig) {
		c.defaultJitter = percent
	}
}

// buildChain constructs a [CycleFunc] that walks the middleware list on each
// call, terminating at handler.RunCycle. The first middleware in the list is
// the outermost (runs first on entry, last on exit).
func buildChain(middlewares []Middleware, handler CycleHandler) CycleFunc {
	final := CycleFunc(handler.RunCycle)
	for i := len(middlewares) - 1; i >= 0; i-- {
		mw := middlewares[i]
		next := final
		final = func(ctx context.Context, info *WorkerInfo) error {
			return mw(ctx, info, next)
		}
	}
	return final
}

// workerRunService wraps the actual Run func as a suture.Service
// that runs inside the worker's own child supervisor.
type workerRunService struct {
	w        *Worker
	runFn    CycleFunc // fully resolved: chain + interval wrapping
	childSup *suture.Supervisor
	metrics  Metrics
	active   *atomic.Int32
	cfg      *runConfig
	attempt  atomic.Int32
}

// closerService calls handler.Close() exactly once when the worker's
// supervisor tree is torn down. It stays alive across restarts — only
// context cancellation (permanent shutdown) triggers Close.
type closerService struct {
	handler CycleHandler
}

func (c *closerService) Serve(ctx context.Context) error {
	<-ctx.Done()
	if c.handler != nil {
		_ = c.handler.Close()
	}
	return suture.ErrDoNotRestart
}

func (c *closerService) String() string { return "closer" }

// Serve implements suture.Service.
func (ws *workerRunService) Serve(ctx context.Context) error {
	attempt := int(ws.attempt.Add(1) - 1)

	m := ws.metrics

	m.WorkerStarted(ws.w.name)
	m.SetActiveWorkers(int(ws.active.Add(1)))

	defer func() {
		m.WorkerStopped(ws.w.name)
		m.SetActiveWorkers(int(ws.active.Add(-1)))
	}()

	if attempt > 0 {
		m.WorkerRestarted(ws.w.name, attempt)
	}

	info := &WorkerInfo{
		name:     ws.w.name,
		attempt:  attempt,
		sup:      ws.childSup,
		children: make(map[string]suture.ServiceToken),
		cfg:      ws.cfg,
		active:   ws.active,
		metrics:  m,
	}

	err := ws.runFn(ctx, info)

	if err != nil && ctx.Err() == nil {
		m.WorkerFailed(ws.w.name, err)
	}

	// Suppress restart when the worker doesn't want it and either exited
	// cleanly or the context was cancelled (graceful shutdown).
	if !ws.w.restartOnFail && (err == nil || ctx.Err() != nil) {
		return suture.ErrDoNotRestart
	}
	return err
}

// String implements fmt.Stringer for suture logging.
func (ws *workerRunService) String() string {
	return ws.w.name
}

// resolveMetrics returns the worker's own metrics if set, otherwise the parent's.
func resolveMetrics(w *Worker, parent Metrics) Metrics {
	if w.metrics != nil {
		return w.metrics
	}
	if parent != nil {
		return parent
	}
	return BaseMetrics{}
}

// addWorkerToSupervisor creates a child supervisor for the worker,
// builds the middleware chain, resolves jitter, and adds the worker
// to the parent supervisor. Returns the service token for removal.
func addWorkerToSupervisor(parent *suture.Supervisor, w *Worker, cfg *runConfig, active *atomic.Int32) suture.ServiceToken {
	m := resolveMetrics(w, cfg.metrics)

	handler := w.handler
	if handler == nil {
		handler = CycleFunc(func(ctx context.Context, _ *WorkerInfo) error {
			<-ctx.Done()
			return ctx.Err()
		})
	}

	// Build middleware chain: run-level → worker-level → handler.RunCycle
	allMiddleware := make([]Middleware, 0, len(cfg.interceptors)+len(w.interceptors))
	allMiddleware = append(allMiddleware, cfg.interceptors...)
	allMiddleware = append(allMiddleware, w.interceptors...)

	runFn := buildChain(allMiddleware, handler)

	// If periodic, wrap with interval/jitter.
	if w.interval > 0 {
		jitter := w.jitterPercent
		if jitter == -1 && cfg.defaultJitter > 0 {
			jitter = cfg.defaultJitter
		}
		if jitter < 0 {
			jitter = 0
		}
		runFn = everyIntervalWithJitter(w.interval, jitter, w.initialDelay, runFn)
	}

	childSup := suture.New("worker:"+w.name, w.sutureSpec(makeEventHook(m)))
	childSup.Add(&workerRunService{
		w: w, runFn: runFn,
		childSup: childSup, metrics: m, active: active, cfg: cfg,
	})
	childSup.Add(&closerService{handler: handler})
	return parent.Add(childSup)
}

// Run starts all workers under a suture supervisor and blocks until ctx is
// cancelled and all workers have exited. Each worker gets its own child
// supervisor — when a worker stops, its children stop too.
// A worker exiting early (without restart) does not stop other workers.
// Returns nil on clean shutdown.
func Run(ctx context.Context, workers []*Worker, opts ...RunOption) error {
	cfg := &runConfig{metrics: BaseMetrics{}, defaultJitter: -1}
	for _, opt := range opts {
		opt(cfg)
	}

	active := &atomic.Int32{}

	root := suture.New("workers", suture.Spec{
		EventHook: makeEventHook(cfg.metrics),
	})
	for _, w := range workers {
		addWorkerToSupervisor(root, w, cfg, active)
	}
	err := root.Serve(ctx)
	if err != nil && ctx.Err() != nil {
		return nil
	}
	return err
}

// RunWorker runs a single worker with panic recovery and optional restart.
// Blocks until ctx is cancelled or the worker exits without RestartOnFail.
func RunWorker(ctx context.Context, w *Worker, opts ...RunOption) {
	_ = Run(ctx, []*Worker{w}, opts...)
}

// makeEventHook returns a suture event hook that logs events and records
// panic metrics.
func makeEventHook(m Metrics) suture.EventHook {
	return func(e suture.Event) {
		em := e.Map()
		switch e.Type() {
		case suture.EventTypeServicePanic:
			name, _ := em["service_name"].(string)
			m.WorkerPanicked(name)
			slog.Error("worker panicked", "worker", em["service_name"], "event", e.String())
		case suture.EventTypeServiceTerminate:
			slog.Warn("worker terminated", "worker", em["service_name"], "event", e.String())
		case suture.EventTypeBackoff:
			slog.Warn("worker backoff", "event", e.String())
		case suture.EventTypeResume:
			slog.Info("worker resumed", "event", e.String())
		case suture.EventTypeStopTimeout:
			slog.Error("worker stop timeout", "worker", em["service_name"], "event", e.String())
		}
	}
}

// Ensure workerRunService implements suture.Service at compile time.
var _ suture.Service = (*workerRunService)(nil)
