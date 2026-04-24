package middleware

import (
	"context"

	"github.com/go-coldbrew/log"
	"github.com/go-coldbrew/workers"
)

// Slog emits structured log lines per cycle via go-coldbrew/log:
// "cycle start" (Info) before the handler, then "cycle end" (Info)
// on success or "cycle error" (Error) on failure. Pair with [LogContext]
// to include worker name and attempt in every log line automatically.
func Slog() workers.Middleware {
	return func(ctx context.Context, info *workers.WorkerInfo, next workers.CycleFunc) error {
		log.Info(ctx, "msg", "cycle start")
		err := next(ctx, info)
		if err != nil {
			log.Error(ctx, "msg", "cycle error", "error", err)
		} else {
			log.Info(ctx, "msg", "cycle end")
		}
		return err
	}
}
