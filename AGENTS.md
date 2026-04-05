# AGENTS.md

## Package Overview

`go-coldbrew/workers` is a worker lifecycle library built on [thejerf/suture](https://github.com/thejerf/suture). It manages background goroutines with panic recovery, configurable restart, tracing, and structured shutdown.

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
│   ├── Worker-A run func
│   ├── Child-A1 supervisor (added via ctx.Add)
│   │   └── Child-A1 run func
│   └── Child-A2 supervisor
│       └── Child-A2 run func
└── Worker-B supervisor
    └── Worker-B run func
```

Key properties:
- **Scoped lifecycle**: when a parent stops, all children stop
- **Independent restart**: each worker restarts independently via suture
- **Panic recovery**: suture catches panics and converts to errors
- **Backoff**: configurable exponential backoff with jitter on restart
- **Tracing**: each worker execution gets an OTEL span via `go-coldbrew/tracing`

## Key Types

- `Worker` — struct with builder pattern (`NewWorker().WithRestart().Every()`)
- `WorkerContext` — extends `context.Context` with `Name()`, `Attempt()`, `Add()`, `Remove()`, `Children()`
- `Run(ctx, []*Worker) error` — starts all workers, blocks until ctx cancelled
- `RunWorker(ctx, *Worker)` — runs a single worker

## Helpers

- `EveryInterval(d, fn)` — periodic ticker loop
- `ChannelWorker[T](ch, fn)` — consume from channel
- `BatchChannelWorker[T](ch, maxSize, maxDelay, fn)` — batch with size/timer flush

## Rules

- Always run `make doc` after changing exported APIs or docstrings
- Always run `make test` and `make lint` before committing
- Don't add `replace` directives to go.mod
