package middleware

import (
	"context"
	"time"

	"github.com/go-coldbrew/workers"
)

// Duration measures wall-clock time of each cycle and calls observe.
func Duration(observe func(name string, d time.Duration)) workers.Middleware {
	return func(ctx context.Context, info *workers.WorkerInfo, next workers.CycleFunc) error {
		start := time.Now()
		err := next(ctx, info)
		observe(info.GetName(), time.Since(start))
		return err
	}
}
