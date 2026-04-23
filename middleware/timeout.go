package middleware

import (
	"context"
	"time"

	"github.com/go-coldbrew/workers"
)

// Timeout returns middleware that enforces a per-cycle deadline.
// If the cycle does not complete within d, the context is cancelled and
// the cycle returns context.DeadlineExceeded.
//
// This is distinct from [workers.Worker.WithTimeout], which controls
// how long the framework waits for graceful shutdown.
func Timeout(d time.Duration) workers.Middleware {
	return func(next workers.CycleHandler) workers.CycleHandler {
		return workers.CycleFunc(func(ctx context.Context, info *workers.WorkerInfo) error {
			ctx, cancel := context.WithTimeout(ctx, d)
			defer cancel()
			return next.RunCycle(ctx, info)
		})
	}
}
