package middleware

import (
	"context"

	"github.com/go-coldbrew/log"
	"github.com/go-coldbrew/tracing"
	"github.com/go-coldbrew/workers"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// Tracing creates an OTEL root span per cycle via go-coldbrew/tracing.
// The span is named "worker:<name>:cycle" and records errors.
// Sampling is determined by the global TracerProvider's sampler.
//
// The OTEL trace ID is injected into the log context as "trace"
// for correlation with the tracing backend.
func Tracing() workers.Middleware {
	return func(ctx context.Context, info *workers.WorkerInfo, next workers.CycleFunc) error {
		span, ctx := tracing.NewInternalSpan(ctx, "worker:"+info.GetName()+":cycle")
		defer span.Finish()

		if spanCtx := oteltrace.SpanFromContext(ctx).SpanContext(); spanCtx.HasTraceID() {
			ctx = log.AddToContext(ctx, "trace", spanCtx.TraceID().String())
		}

		err := next(ctx, info)
		if err != nil {
			_ = span.SetError(err)
		}
		return err
	}
}
