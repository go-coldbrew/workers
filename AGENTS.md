# AGENTS.md

## Package Overview

`go-coldbrew/workers` is a worker lifecycle library built on [thejerf/suture](https://github.com/thejerf/suture). It manages background goroutines with panic recovery, configurable restart, composable middleware, jitter, and structured shutdown.

## Build & Test

```bash
make build    # go build ./...
make test     # go test -race ./...
make lint     # golangci-lint + govulncheck
make bench    # benchmarks
make doc      # regenerate README.md via gomarkdoc
```

## Architecture

Every worker runs inside its own suture supervisor subtree:

```
Root Supervisor (created by Run)
├── Worker-A supervisor
│   ├── Worker-A service (middleware chain → handler)
│   ├── Child-A1 supervisor (added via info.Add)
│   │   └── Child-A1 service
│   └── Child-A2 supervisor
│       └── Child-A2 service
└── Worker-B supervisor
    └── Worker-B service
```

Key properties:
- **Scoped lifecycle**: when a parent stops, all children stop
- **Independent restart**: each worker restarts independently via suture
- **Panic recovery**: suture catches panics and converts to errors
- **Backoff**: configurable exponential backoff with jitter on restart
- **Middleware chain**: run-level → worker-level → handler (gRPC interceptor convention)

## Key Types

- `WorkerInfo` — struct with `Name`, `Attempt`, and child management (`Add`, `Remove`, `Children`)
- `CycleHandler` — interface: `RunCycle(ctx, *WorkerInfo) error` + `Close() error`
- `CycleFunc` — function adapter for `CycleHandler` (Close is no-op)
- `Middleware` — `func(ctx, *WorkerInfo, next CycleFunc) error` (interceptor pattern)
- `Worker` — builder pattern: `NewWorker(name).HandlerFunc(fn).Every(d).WithJitter(10).Interceptors(mw...)`
- `Run(ctx, []*Worker, ...RunOption) error` — starts all workers, blocks until ctx cancelled
- `RunWorker(ctx, *Worker, ...RunOption)` — runs a single worker

## Middleware Sub-Package

`workers/middleware` provides optional built-in interceptors:
- `Recover(onPanic)` — catch panics per-cycle
- `Tracing()` — OTEL span per cycle
- `Duration(observe)` — wall-clock timing
- `Timeout(d)` — per-cycle deadline
- `Slog()` — structured log per cycle
- `LogContext()` — inject worker name/attempt into log context
- `DistributedLock(locker, opts...)` — distributed lock before each cycle
- `DefaultInterceptors()` — [Recover, LogContext, Tracing, Slog]

## Helpers

- `EveryInterval(d, fn)` — periodic timer loop
- `ChannelWorker[T](ch, fn)` — consume from channel
- `BatchChannelWorker[T](ch, maxSize, maxDelay, fn)` — batch with size/timer flush

## Rules

- Always run `make doc` after changing exported APIs or docstrings
- Always run `make test` and `make lint` before committing
- Don't add `replace` directives to go.mod
