package workers

import (
	"context"
	"sync"

	"github.com/go-coldbrew/log"
	"github.com/go-coldbrew/tracing"
	"github.com/thejerf/suture/v4"
)

// workerRunService wraps the actual Run func as a suture.Service
// that runs inside the worker's own child supervisor.
type workerRunService struct {
	w        *Worker
	childSup *suture.Supervisor
	mu       sync.Mutex
	attempt  int
}

// Serve implements suture.Service.
func (ws *workerRunService) Serve(ctx context.Context) error {
	ws.mu.Lock()
	attempt := ws.attempt
	ws.attempt++
	ws.mu.Unlock()

	span, ctx := tracing.NewInternalSpan(ctx, "worker:"+ws.w.name)
	defer span.Finish()

	// Inject worker name and attempt into log context so all log calls
	// inside the worker automatically include them.
	ctx = log.AddToContext(ctx, "worker", ws.w.name)
	ctx = log.AddToContext(ctx, "attempt", attempt)

	wctx := newWorkerContext(ctx, ws.w.name, attempt, ws.childSup)
	err := ws.w.run(wctx)

	if !ws.w.restartOnFail && (err == nil || ctx.Err() != nil) {
		return suture.ErrDoNotRestart
	}
	return err
}

// String implements fmt.Stringer for suture logging.
func (ws *workerRunService) String() string {
	return ws.w.name
}

// addWorkerToSupervisor creates a child supervisor for the worker,
// adds the worker's run func as a service inside it, and adds the
// child supervisor to the parent. Returns the service token for removal.
func addWorkerToSupervisor(parent *suture.Supervisor, w *Worker) suture.ServiceToken {
	childSup := suture.New("worker:"+w.name, w.sutureSpec(logEventHook))
	childSup.Add(&workerRunService{w: w, childSup: childSup})
	return parent.Add(childSup)
}

// Run starts all workers under a suture supervisor and blocks until ctx is
// cancelled and all workers have exited. Each worker gets its own child
// supervisor — when a worker stops, its children stop too.
// A worker exiting early (without restart) does not stop other workers.
// Returns nil on clean shutdown.
func Run(ctx context.Context, workers []*Worker) error {
	root := suture.New("workers", suture.Spec{
		EventHook: logEventHook,
	})
	for _, w := range workers {
		addWorkerToSupervisor(root, w)
	}
	err := root.Serve(ctx)
	if err != nil && ctx.Err() != nil {
		return nil
	}
	return err
}

// RunWorker runs a single worker with panic recovery and optional restart.
// Blocks until ctx is cancelled or the worker exits without RestartOnFail.
func RunWorker(ctx context.Context, w *Worker) {
	_ = Run(ctx, []*Worker{w})
}

// logEventHook logs suture supervisor events via go-coldbrew/log.
func logEventHook(e suture.Event) {
	ctx := context.Background()
	m := e.Map()
	switch e.Type() {
	case suture.EventTypeServicePanic:
		log.Error(ctx, "msg", "worker panicked", "worker", m["service_name"], "event", e.String())
	case suture.EventTypeServiceTerminate:
		log.Warn(ctx, "msg", "worker terminated", "worker", m["service_name"], "event", e.String())
	case suture.EventTypeBackoff:
		log.Warn(ctx, "msg", "worker backoff", "event", e.String())
	case suture.EventTypeResume:
		log.Info(ctx, "msg", "worker resumed", "event", e.String())
	case suture.EventTypeStopTimeout:
		log.Error(ctx, "msg", "worker stop timeout", "worker", m["service_name"], "event", e.String())
	}
}
