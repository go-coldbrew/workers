package workers

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-coldbrew/log"
	"github.com/go-coldbrew/tracing"
	"github.com/thejerf/suture/v4"
)

// RunOption configures the behavior of Run.
type RunOption func(*runConfig)

type runConfig struct {
	metrics       Metrics
	defaultJitter *int
	middleware    []Middleware
}

// WithMetrics sets the metrics implementation for all workers started by Run.
// Workers inherit this unless they override via Worker.WithMetrics.
// If not set, BaseMetrics{} is used.
func WithMetrics(m Metrics) RunOption {
	return func(c *runConfig) {
		if m != nil {
			c.metrics = m
		}
	}
}

// WithDefaultJitter sets the default jitter percentage for all periodic workers
// started by Run. Workers that call WithJitter override this value.
// Has no effect on non-periodic workers.
func WithDefaultJitter(percent int) RunOption {
	return func(c *runConfig) {
		c.defaultJitter = &percent
	}
}

// WithMiddleware sets run-level middleware applied to all workers.
// Run-level middleware wraps outside worker-level middleware (set via Use),
// so run-level concerns like tracing are always outermost.
func WithMiddleware(mw ...Middleware) RunOption {
	return func(c *runConfig) {
		c.middleware = append(c.middleware, mw...)
	}
}

// workerRunService wraps the actual Run func as a suture.Service
// that runs inside the worker's own child supervisor.
type workerRunService struct {
	w        *Worker
	chain    CycleHandler              // middleware chain (RunCycle per tick, Close on stop)
	runFn    func(WorkerContext) error // interval loop wrapping chain.RunCycle
	childSup *suture.Supervisor
	cfg      *runConfig
	active   *atomic.Int32
	mu       sync.Mutex
	attempt  int
}

// Serve implements suture.Service.
func (ws *workerRunService) Serve(ctx context.Context) error {
	ws.mu.Lock()
	attempt := ws.attempt
	ws.attempt++
	ws.mu.Unlock()

	m := ws.cfg.metrics

	m.WorkerStarted(ws.w.name)
	m.SetActiveWorkers(int(ws.active.Add(1)))

	start := time.Now()
	defer func() {
		m.WorkerStopped(ws.w.name)
		m.SetActiveWorkers(int(ws.active.Add(-1)))
		m.ObserveRunDuration(ws.w.name, time.Since(start))
	}()

	if attempt > 0 {
		m.WorkerRestarted(ws.w.name, attempt)
	}

	span, ctx := tracing.NewInternalSpan(ctx, "worker:"+ws.w.name)
	defer span.Finish()

	// Inject worker name and attempt into log context so all log calls
	// inside the worker automatically include them.
	ctx = log.AddToContext(ctx, "worker", ws.w.name)
	ctx = log.AddToContext(ctx, "attempt", attempt)

	// Inject WorkerInfo into context for deep callstacks (convenience).
	ctx = WithWorkerInfo(ctx, WorkerInfo{Name: ws.w.name, Attempt: attempt})

	// Close the middleware chain when the worker stops (flush, release).
	defer ws.chain.Close()

	wctx := newWorkerContext(ctx, ws.w.name, attempt, ws.childSup, ws.cfg, ws.active)
	err := ws.runFn(wctx)

	if err != nil && ctx.Err() == nil {
		m.WorkerFailed(ws.w.name, err)
	}

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

// resolveJitter returns the effective jitter percentage for a worker.
// Worker-level WithJitter takes precedence over run-level WithDefaultJitter.
func resolveJitter(w *Worker, cfg *runConfig) int {
	if w.jitterPercent != nil {
		return *w.jitterPercent
	}
	if cfg != nil && cfg.defaultJitter != nil {
		return *cfg.defaultJitter
	}
	return 0
}

// addWorkerToSupervisor creates a child supervisor for the worker,
// adds the worker's run func as a service inside it, and adds the
// child supervisor to the parent. Returns the service token for removal.
func addWorkerToSupervisor(parent *suture.Supervisor, w *Worker, cfg *runConfig, active *atomic.Int32) suture.ServiceToken {
	m := resolveMetrics(w, cfg.metrics)

	// Build a runConfig scoped to this worker (inherits run-level settings,
	// overrides metrics if the worker has its own).
	workerCfg := &runConfig{
		metrics:       m,
		defaultJitter: cfg.defaultJitter,
		middleware:    cfg.middleware,
	}

	// Build middleware chain wrapping the original run function.
	chain := buildChain(cfg.middleware, w.middlewares, w.run)

	// Build the per-tick function that the interval loop calls.
	// This bridges from WorkerContext (used by the interval loop) to
	// CycleHandler.RunCycle (used by middleware), threading WorkerContext
	// through a context value for the innermost adapter.
	info := &WorkerInfo{Name: w.name}
	tickFn := func(wctx WorkerContext) error {
		ctx := context.WithValue(wctx, wctxKey{}, wctx)
		info.Attempt = wctx.Attempt()
		return chain.RunCycle(ctx, info)
	}

	// Wrap with Every if interval is set.
	var runFn func(WorkerContext) error
	if w.interval > 0 {
		jitter := resolveJitter(w, cfg)
		if jitter > 0 {
			runFn = everyIntervalWithJitter(w.interval, jitter, w.initialDelay, tickFn)
		} else {
			runFn = everyIntervalWithDelay(w.interval, w.initialDelay, tickFn)
		}
	} else {
		runFn = tickFn
	}

	childSup := suture.New("worker:"+w.name, w.sutureSpec(makeEventHook(m)))
	childSup.Add(&workerRunService{w: w, chain: chain, runFn: runFn, childSup: childSup, cfg: workerCfg, active: active})
	return parent.Add(childSup)
}

// Run starts all workers under a suture supervisor and blocks until ctx is
// cancelled and all workers have exited. Each worker gets its own child
// supervisor — when a worker stops, its children stop too.
// A worker exiting early (without restart) does not stop other workers.
// Returns nil on clean shutdown.
func Run(ctx context.Context, workers []*Worker, opts ...RunOption) error {
	cfg := &runConfig{metrics: BaseMetrics{}}
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
		ctx := context.Background()
		em := e.Map()
		switch e.Type() {
		case suture.EventTypeServicePanic:
			name, _ := em["service_name"].(string)
			m.WorkerPanicked(name)
			log.Error(ctx, "msg", "worker panicked", "worker", em["service_name"], "event", e.String())
		case suture.EventTypeServiceTerminate:
			log.Warn(ctx, "msg", "worker terminated", "worker", em["service_name"], "event", e.String())
		case suture.EventTypeBackoff:
			log.Warn(ctx, "msg", "worker backoff", "event", e.String())
		case suture.EventTypeResume:
			log.Info(ctx, "msg", "worker resumed", "event", e.String())
		case suture.EventTypeStopTimeout:
			log.Error(ctx, "msg", "worker stop timeout", "worker", em["service_name"], "event", e.String())
		}
	}
}
