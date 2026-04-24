package middleware

import (
	"context"

	"github.com/go-coldbrew/log"
	"github.com/go-coldbrew/workers"
)

// LogContext injects worker name and attempt into the log context so all
// log calls inside the worker automatically include them.
func LogContext() workers.Middleware {
	return func(ctx context.Context, info *workers.WorkerInfo, next workers.CycleFunc) error {
		ctx = log.AddToContext(ctx, "worker", info.GetName())
		ctx = log.AddToContext(ctx, "attempt", info.GetAttempt())
		return next(ctx, info)
	}
}
