package middleware

import (
	"context"
	"time"

	"github.com/go-coldbrew/workers"
)

// Duration returns middleware that measures the wall-clock duration of each
// worker cycle and calls observe with the worker name and elapsed time.
// This is a building block for custom metrics — it keeps the library
// decoupled from any specific metrics system.
// If observe is nil, the middleware is a no-op passthrough.
func Duration(observe func(name string, d time.Duration)) workers.Middleware {
	if observe == nil {
		return func(next workers.CycleHandler) workers.CycleHandler { return next }
	}
	return func(next workers.CycleHandler) workers.CycleHandler {
		return workers.CycleFunc(func(ctx context.Context, info *workers.WorkerInfo) error {
			start := time.Now()
			err := next.RunCycle(ctx, info)
			observe(info.Name, time.Since(start))
			return err
		})
	}
}
