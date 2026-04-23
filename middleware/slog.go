package middleware

import (
	"context"
	"time"

	"github.com/go-coldbrew/log"
	"github.com/go-coldbrew/workers"
)

// Slog returns middleware that emits a structured log line for each worker cycle
// using [log] (go-coldbrew/log).
//
// On success (nil error), logs at Info level with worker name and duration.
// On failure (non-nil error), logs at Error level with worker name, duration,
// and error message.
func Slog() workers.Middleware {
	return func(next workers.CycleHandler) workers.CycleHandler {
		return workers.CycleFunc(func(ctx context.Context, info *workers.WorkerInfo) error {
			start := time.Now()
			err := next.RunCycle(ctx, info)
			d := time.Since(start)
			if err != nil {
				log.Error(ctx, "msg", "worker cycle failed",
					"worker", info.Name,
					"duration", d.String(),
					"error", err.Error(),
				)
			} else {
				log.Info(ctx, "msg", "worker cycle completed",
					"worker", info.Name,
					"duration", d.String(),
				)
			}
			return err
		})
	}
}
