package workers

import "context"

// CycleHandler handles worker execution cycles. Implement as a struct for
// middleware with lifecycle needs (flush buffers, release resources).
// For simple, stateless middleware, use [CycleFunc] instead.
type CycleHandler interface {
	// RunCycle executes one cycle of the worker.
	// For periodic workers (Every), this is called once per tick.
	RunCycle(ctx context.Context, info *WorkerInfo) error
	// Close is called once when the worker stops. Use it to flush buffers,
	// release connections, or clean up resources acquired during cycles.
	Close() error
}

// CycleFunc adapts a plain function into a [CycleHandler].
// Close is a no-op — use this for simple, stateless middleware.
type CycleFunc func(ctx context.Context, info *WorkerInfo) error

// RunCycle implements [CycleHandler].
func (fn CycleFunc) RunCycle(ctx context.Context, info *WorkerInfo) error { return fn(ctx, info) }

// Close implements [CycleHandler] as a no-op.
func (fn CycleFunc) Close() error { return nil }

// Middleware wraps a [CycleHandler] to add cross-cutting behavior.
// The first middleware in a list is the outermost wrapper (runs first on
// entry, last on exit), matching the convention of gRPC interceptors.
type Middleware func(next CycleHandler) CycleHandler

// WorkerInfo carries worker metadata through the middleware chain.
// Passed as a pointer for future extensibility — the framework always
// creates it (never nil).
type WorkerInfo struct {
	Name    string
	Attempt int
}

type workerInfoKey struct{}

// WithWorkerInfo returns a copy of ctx with the given WorkerInfo attached.
// The framework injects this automatically; use [FromContext] in deep
// callstacks where the explicit *WorkerInfo parameter is not available.
func WithWorkerInfo(ctx context.Context, info WorkerInfo) context.Context {
	return context.WithValue(ctx, workerInfoKey{}, info)
}

// FromContext extracts WorkerInfo from a context.
// Returns a zero-value WorkerInfo if not present.
// This is a convenience for deep callstacks — middleware should use the
// explicit *WorkerInfo parameter passed to RunCycle instead.
func FromContext(ctx context.Context) WorkerInfo {
	info, _ := ctx.Value(workerInfoKey{}).(WorkerInfo)
	return info
}

// chainHandler composes a RunCycle chain with aggregated Close calls.
type chainHandler struct {
	handler CycleHandler
	closers []func() error // Close funcs collected during chain build
}

func (c *chainHandler) RunCycle(ctx context.Context, info *WorkerInfo) error {
	return c.handler.RunCycle(ctx, info)
}

func (c *chainHandler) Close() error {
	var firstErr error
	for _, fn := range c.closers {
		if err := fn(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// buildChain composes run-level and worker-level middleware around the
// original run function. Run-level middleware wraps outside worker-level
// middleware. Returns the original function (as a CycleHandler) unchanged
// if no middleware is set.
//
// The returned CycleHandler's Close() calls each middleware's Close() in
// reverse build order (innermost first, matching deferred-cleanup semantics).
func buildChain(runLevel, workerLevel []Middleware, original func(WorkerContext) error) CycleHandler {
	// The innermost CycleHandler adapts from CycleHandler to the
	// WorkerContext-based run function. WorkerContext is threaded
	// through a context value so the adapter can recover it.
	var inner CycleHandler = CycleFunc(func(ctx context.Context, _ *WorkerInfo) error {
		wctx := ctx.Value(wctxKey{}).(WorkerContext)
		return original(wctx)
	})

	if len(runLevel) == 0 && len(workerLevel) == 0 {
		return inner
	}

	// Collect closers in build order (innermost first).
	var closers []func() error

	// Apply worker-level middleware (innermost, closest to the function).
	for i := len(workerLevel) - 1; i >= 0; i-- {
		inner = workerLevel[i](inner)
		closers = append(closers, inner.Close)
	}
	// Apply run-level middleware (outermost).
	for i := len(runLevel) - 1; i >= 0; i-- {
		inner = runLevel[i](inner)
		closers = append(closers, inner.Close)
	}

	return &chainHandler{handler: inner, closers: closers}
}

// wctxKey is a context key for threading WorkerContext through the
// middleware chain. This is an internal bridge — middleware authors
// use the explicit *WorkerInfo parameter, not this key.
type wctxKey struct{}
