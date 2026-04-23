# Workers: Design Document

## Goal

Add jitter support and a composable middleware chain to `go-coldbrew/workers`, letting consumers write worker bodies as pure business logic with no lifecycle plumbing.

```go
w := workers.NewWorker("route-solver").
    HandlerFunc(solve).
    Every(15 * time.Second).
    WithJitter(10).
    Interceptors(
        middleware.Recover(onPanic),
        middleware.Tracing(),
        middleware.DistributedLock(redisLocker),
    )
```

---

## Part 1 — Jitter on Every()

### Problem

When many workers share the same base interval (e.g. 15s), they synchronize and spike downstream services — the thundering herd problem.

### API

- `WithJitter(percent int) *Worker` — per-worker jitter. Each tick is randomized within ±percent of the base.
- `WithDefaultJitter(percent int) RunOption` — run-level default. Worker-level takes precedence.
- `WithInitialDelay(d time.Duration) *Worker` — delays the first tick to stagger startup.

### Behavior

On each tick: `spread = base × percent ÷ 100; jittered = base − spread + rand(2 × spread)`. Clamped to minimum 1ms. Uses `time.NewTimer` + `Reset` (not `time.Ticker`) for variable intervals. `Every()` stores the interval as data — wrapping is deferred to startup where jitter config is resolved.

---

## Part 2 — Core Types

### WorkerInfo

`WorkerInfo` is the single type carrying all worker metadata and child management. Passed as a pointer for future extensibility. The framework always creates it (never nil).

```go
type WorkerInfo struct {
    Name    string
    Attempt int

    // unexported — child management, set by framework
    sup      *suture.Supervisor
    children *sync.Map
    cfg      *runConfig
    active   *atomic.Int32
}

func (info *WorkerInfo) Add(w *Worker)        { ... }
func (info *WorkerInfo) Remove(name string)   { ... }
func (info *WorkerInfo) Children() []string    { ... }
```

This replaces `WorkerContext` entirely. No overlap — `context.Context` handles cancellation/deadlines/values, `*WorkerInfo` handles everything worker-specific.

### CycleHandler — worker body interface

```go
// CycleHandler handles worker execution cycles.
// For periodic workers, RunCycle is called once per tick.
type CycleHandler interface {
    RunCycle(ctx context.Context, info *WorkerInfo) error
    Close() error  // called once when the worker stops
}

// CycleFunc adapts a plain function. Close is a no-op.
type CycleFunc func(ctx context.Context, info *WorkerInfo) error

func (fn CycleFunc) RunCycle(ctx context.Context, info *WorkerInfo) error { return fn(ctx, info) }
func (fn CycleFunc) Close() error                                        { return nil }
```

`Close()` is on the worker handler, not on middleware. Workers that acquire resources (DB connections, leases) implement `CycleHandler` as a struct with cleanup. Simple workers use `CycleFunc`.

### Middleware — interceptor pattern

```go
// Middleware intercepts each execution cycle.
// Call next to continue the chain. Matches gRPC interceptor convention.
type Middleware func(ctx context.Context, info *WorkerInfo, next CycleFunc) error
```

Flat function — no nesting, no interface. Developers write normal Go code:

```go
func myLogging(ctx context.Context, info *workers.WorkerInfo, next workers.CycleFunc) error {
    log.Info(ctx, "msg", "cycle start", "worker", info.Name)
    err := next(ctx, info)
    log.Info(ctx, "msg", "cycle end", "worker", info.Name, "error", err)
    return err
}
```

Same shape as gRPC interceptors — familiar to the target audience:
```go
// gRPC:   func(ctx, req, info, handler) (resp, error)
// Workers: func(ctx, info, next) error
```

### NewWorker — builder pattern

```go
func NewWorker(name string) *Worker

// Set handler — choose one:
func (w *Worker) Handler(h CycleHandler) *Worker       // interface (struct with Close)
func (w *Worker) HandlerFunc(fn CycleFunc) *Worker      // function (common case)

// Configure:
func (w *Worker) Every(d time.Duration) *Worker
func (w *Worker) WithJitter(percent int) *Worker
func (w *Worker) WithInitialDelay(d time.Duration) *Worker
func (w *Worker) Interceptors(mw ...Middleware) *Worker      // replaces the list
func (w *Worker) AddInterceptors(mw ...Middleware) *Worker   // appends to the list
func (w *Worker) WithRestart(restart bool) *Worker
// ... existing builder methods unchanged ...
```

Usage:

```go
// Simple periodic worker
workers.NewWorker("poller").
    HandlerFunc(poll).
    Every(15 * time.Second).
    WithJitter(10)

// Manager worker — child management via info
workers.NewWorker("manager").
    HandlerFunc(func(ctx context.Context, info *workers.WorkerInfo) error {
        info.Add(workers.NewWorker("child").HandlerFunc(childFn))
        <-ctx.Done()
        return ctx.Err()
    })

// Struct-based handler with cleanup
workers.NewWorker("batch").
    Handler(&batchProcessor{db: db}).
    Every(30 * time.Second)
```

---

## Part 3 — Middleware Chain

### Chain Ordering

```
run-level middleware → worker-level middleware → handler.RunCycle
```

First middleware in the list is outermost (runs first on entry, last on exit). Matches gRPC interceptor convention.

### Chain Building

The chain is built at startup in `addWorkerToSupervisor`. Middleware list is stored, and a `CycleFunc` is constructed that walks the list on each tick:

```go
func buildChain(middlewares []Middleware, handler CycleHandler) CycleFunc {
    final := handler.RunCycle
    for i := len(middlewares) - 1; i >= 0; i-- {
        mw := middlewares[i]
        next := final
        final = func(ctx context.Context, info *WorkerInfo) error {
            return mw(ctx, info, next)
        }
    }
    return final
}
```

### Worker API

**Worker-level:**

- `Interceptors(mw ...Middleware) *Worker` — **replaces** the interceptor list.
- `AddInterceptors(mw ...Middleware) *Worker` — **appends** to the interceptor list.

**Run-level (RunOption):**

- `WithInterceptors(mw ...Middleware) RunOption` — **replaces** run-level interceptors.
- `AddInterceptors(mw ...Middleware) RunOption` — **appends** to run-level interceptors.

```go
// Set a base stack, then extend
w.Interceptors(baseStack...).AddInterceptors(extraMiddleware)

// Run-level: set defaults for all workers, add more per-run
workers.Run(ctx, myWorkers,
    workers.WithInterceptors(standardStack...),
    workers.AddInterceptors(middleware.Slog()),
)
```

---

## Part 4 — Slim Serve()

Cross-cutting concerns that were hardcoded in `Serve()` move to middleware:

| Currently hardcoded in Serve | Becomes |
|-----|---------|
| `tracing.NewInternalSpan(ctx, ...)` | `middleware.Tracing()` |
| `log.AddToContext(ctx, "worker", ...)` | `middleware.Slog()` or log-context middleware |
| `ObserveRunDuration(...)` | `middleware.Duration(observe)` |

What stays in `Serve()` — per-lifetime metrics only:

```go
func (ws *workerRunService) Serve(ctx context.Context) error {
    // attempt tracking
    // WorkerStarted / WorkerStopped / WorkerRestarted / SetActiveWorkers
    // create *WorkerInfo
    // defer handler.Close()
    return ws.runFn(ctx, info)
}
```

This lets the core `workers` package drop direct dependencies on `go-coldbrew/tracing` and `go-coldbrew/log` — those imports move to the `middleware/` sub-package only.

---

## Part 5 — Built-in Middleware

Ship as `github.com/go-coldbrew/workers/middleware`. All optional, none applied by default.

| Middleware | Purpose |
|-----------|---------|
| `Recover(onPanic)` | Catches panics per-cycle, calls callback, returns error |
| `Tracing()` | OTEL span per cycle via go-coldbrew/tracing |
| `Duration(observe)` | Wall-clock timing callback |
| `DistributedLock(locker, opts...)` | Distributed lock with Locker interface |
| `Timeout(d)` | Per-cycle deadline |
| `Slog()` | Structured log per cycle via go-coldbrew/log |
| `LogContext()` | Injects worker name + attempt into log context |

Example — each is a flat function:

```go
func Tracing() workers.Middleware {
    return func(ctx context.Context, info *workers.WorkerInfo, next workers.CycleFunc) error {
        span, ctx := tracing.NewInternalSpan(ctx, "worker:"+info.Name+":cycle")
        defer span.Finish()
        err := next(ctx, info)
        if err != nil {
            span.SetError(err)
        }
        return err
    }
}

func Timeout(d time.Duration) workers.Middleware {
    return func(ctx context.Context, info *workers.WorkerInfo, next workers.CycleFunc) error {
        ctx, cancel := context.WithTimeout(ctx, d)
        defer cancel()
        return next(ctx, info)
    }
}
```

### DistributedLock Details

```go
type Locker interface {
    Acquire(ctx context.Context, key string, ttl time.Duration) (bool, error)
    Release(ctx context.Context, key string) error
}
```

Options: `WithKeyFunc`, `WithTTLFunc`, `WithOnNotAcquired`. Defaults: key = `"worker-lock:"+name`, TTL = 30s, not-acquired = skip silently. Release uses `context.WithoutCancel` to prevent cancellation from blocking cleanup.

### DefaultInterceptors

Convenience bundle for the standard observability stack. Returns a slice so it composes with both worker-level and run-level APIs.

```go
func DefaultInterceptors() []workers.Middleware {
    return []workers.Middleware{
        Recover(nil),
        LogContext(),
        Tracing(),
        Slog(),
    }
}
```

Usage:

```go
// Zero-config observability — one line
workers.Run(ctx, myWorkers,
    workers.WithInterceptors(middleware.DefaultInterceptors()...),
)

// Defaults + extras
workers.Run(ctx, myWorkers,
    workers.WithInterceptors(middleware.DefaultInterceptors()...),
    workers.AddInterceptors(middleware.Duration(observe)),
)

// Per-worker
workers.NewWorker("special").
    HandlerFunc(fn).
    Interceptors(middleware.DefaultInterceptors()...).
    AddInterceptors(middleware.DistributedLock(locker))

// Custom stack — skip defaults entirely
workers.Run(ctx, myWorkers,
    workers.WithInterceptors(middleware.Tracing()),
)
```

---

## Design Decisions Summary

| Decision | Choice | Why |
|----------|--------|-----|
| Worker metadata | `*WorkerInfo` (replaces `WorkerContext`) | No overlap — ctx handles context, info handles worker |
| Child management | Methods on `*WorkerInfo` | Available everywhere, opt-in by usage, no bridge function |
| Middleware pattern | Interceptor `func(ctx, info, next)` | Flat, no nesting, matches gRPC, familiar to target audience |
| Middleware cleanup | Not needed — `Close()` on handler only | Middleware is stateless; workers own resources |
| NewWorker | `NewWorker(name)` + `.Handler`/`.HandlerFunc` | Both interface and function paths, explicit choice |
| Serve() | Slim — lifecycle metrics only | Tracing/logging move to middleware, drops core deps |
| Every() wrapping | Deferred to startup | Required for jitter resolution |
| `*WorkerInfo` pointer | Yes | Future extensibility, consistent with asynq/gRPC |

---

## Non-goals

- Retry middleware — users handle retries in business logic or via supervisor restart.
- Async/fan-out — the chain is strictly sequential.
- `Close()` on middleware — middleware is stateless; only worker handlers need cleanup.
- Unifying `NewWorker` to accept only `CycleHandler` — `.Handler`/`.HandlerFunc` builder methods give both options explicitly.
