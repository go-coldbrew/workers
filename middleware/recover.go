// Package middleware provides composable middleware for go-coldbrew/workers.
//
// Middleware wraps each worker execution cycle with cross-cutting concerns
// like panic recovery, tracing, distributed locking, and timing. None are
// applied by default — import and compose them via [workers.Worker.Use] or
// [workers.WithMiddleware].
package middleware

import (
	"context"
	"fmt"

	"github.com/go-coldbrew/workers"
)

// Recover returns middleware that catches panics in the worker cycle.
// On panic, it calls onPanic with the worker name and recovered value,
// then returns an error wrapping the panic value. The panic is not propagated.
//
// This is distinct from suture's supervisor-level panic recovery: Recover
// operates per-cycle for periodic workers, allowing the ticker loop to
// continue after a single tick panics.
func Recover(onPanic func(name string, v any)) workers.Middleware {
	return func(next workers.CycleHandler) workers.CycleHandler {
		return workers.CycleFunc(func(ctx context.Context, info *workers.WorkerInfo) (err error) {
			defer func() {
				if v := recover(); v != nil {
					if onPanic != nil {
						onPanic(info.Name, v)
					}
					err = fmt.Errorf("worker %s panicked: %v", info.Name, v)
				}
			}()
			return next.RunCycle(ctx, info)
		})
	}
}
