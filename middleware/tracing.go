package middleware

import (
	"context"

	"github.com/go-coldbrew/tracing"
	"github.com/go-coldbrew/workers"
)

// Tracing creates an OTEL span per cycle via go-coldbrew/tracing.
// The span is named "worker:<name>:cycle" and records errors.
func Tracing() workers.Middleware {
	return func(ctx context.Context, info *workers.WorkerInfo, next workers.CycleFunc) error {
		span, ctx := tracing.NewInternalSpan(ctx, "worker:"+info.GetName()+":cycle")
		defer span.Finish()
		err := next(ctx, info)
		if err != nil {
			_ = span.SetError(err)
		}
		return err
	}
}
