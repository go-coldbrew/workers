package middleware

import (
	"context"
	"time"

	"github.com/go-coldbrew/workers"
)

// Timeout enforces a per-cycle deadline. Distinct from [workers.Worker.WithTimeout]
// which controls graceful shutdown.
func Timeout(d time.Duration) workers.Middleware {
	return func(ctx context.Context, info *workers.WorkerInfo, next workers.CycleFunc) error {
		ctx, cancel := context.WithTimeout(ctx, d)
		defer cancel()
		return next(ctx, info)
	}
}
