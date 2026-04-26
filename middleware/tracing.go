package middleware

import (
	"context"
	"crypto/rand"

	"github.com/go-coldbrew/log"
	"github.com/go-coldbrew/tracing"
	"github.com/go-coldbrew/workers"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// Tracing creates an OTEL span per cycle via go-coldbrew/tracing.
// The span is named "worker:<name>:cycle" and records errors.
//
// Worker spans are always sampled regardless of the global
// TracerProvider's sampler. This prevents silent span drops when
// using ParentBased(TraceIDRatioBased(ratio)), where worker root
// spans (which have no parent) would otherwise be probabilistically
// dropped.
//
// The OTEL trace ID is injected into the log context as "trace"
// for correlation with the tracing backend.
func Tracing() workers.Middleware {
	return func(ctx context.Context, info *workers.WorkerInfo, next workers.CycleFunc) error {
		ctx = ensureSampled(ctx)
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

// ensureSampled injects a sampled remote span context so that
// ParentBased samplers always sample the next span created from
// this context. If the context already has a sampled span, it is
// returned unchanged.
func ensureSampled(ctx context.Context) context.Context {
	if oteltrace.SpanFromContext(ctx).SpanContext().IsSampled() {
		return ctx
	}
	var traceID oteltrace.TraceID
	var spanID oteltrace.SpanID
	_, _ = rand.Read(traceID[:])
	_, _ = rand.Read(spanID[:])
	return oteltrace.ContextWithRemoteSpanContext(ctx, oteltrace.NewSpanContext(oteltrace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     spanID,
		TraceFlags: oteltrace.FlagsSampled,
		Remote:     true,
	}))
}
