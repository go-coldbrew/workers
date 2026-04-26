package middleware

import (
	"context"
	"testing"

	"github.com/go-coldbrew/workers"
	"github.com/stretchr/testify/assert"
	oteltrace "go.opentelemetry.io/otel/trace"
)

func TestTracing_NoPanic(t *testing.T) {
	mw := Tracing()

	called := false
	info := workers.NewWorkerInfo("traced", 0)
	err := mw(context.Background(), info, func(_ context.Context, _ *workers.WorkerInfo) error {
		called = true
		return nil
	})

	assert.NoError(t, err)
	assert.True(t, called)
}

func TestTracing_PropagatesError(t *testing.T) {
	mw := Tracing()

	info := workers.NewWorkerInfo("traced", 0)
	err := mw(context.Background(), info, func(_ context.Context, _ *workers.WorkerInfo) error {
		return assert.AnError
	})

	assert.ErrorIs(t, err, assert.AnError)
}

func TestEnsureSampled(t *testing.T) {
	// From a bare context (no span), ensureSampled should inject a
	// sampled remote span context.
	ctx := ensureSampled(context.Background())
	sc := oteltrace.SpanContextFromContext(ctx)

	assert.True(t, sc.IsSampled(), "should be sampled")
	assert.True(t, sc.IsRemote(), "should be remote")
	assert.True(t, sc.HasTraceID(), "should have a trace ID")
	assert.True(t, sc.HasSpanID(), "should have a span ID")
}

func TestEnsureSampled_AlreadySampled(t *testing.T) {
	// If context already has a sampled span, ensureSampled is a no-op.
	existing := oteltrace.NewSpanContext(oteltrace.SpanContextConfig{
		TraceID:    oteltrace.TraceID{1, 2, 3},
		SpanID:     oteltrace.SpanID{4, 5, 6},
		TraceFlags: oteltrace.FlagsSampled,
		Remote:     true,
	})
	ctx := oteltrace.ContextWithRemoteSpanContext(context.Background(), existing)

	ctx = ensureSampled(ctx)
	sc := oteltrace.SpanContextFromContext(ctx)

	assert.Equal(t, existing.TraceID(), sc.TraceID(), "should keep existing trace ID")
	assert.Equal(t, existing.SpanID(), sc.SpanID(), "should keep existing span ID")
}

func TestEnsureSampled_UniqueIDs(t *testing.T) {
	ctx1 := ensureSampled(context.Background())
	ctx2 := ensureSampled(context.Background())

	sc1 := oteltrace.SpanContextFromContext(ctx1)
	sc2 := oteltrace.SpanContextFromContext(ctx2)

	assert.NotEqual(t, sc1.TraceID(), sc2.TraceID(), "each call should generate unique trace IDs")
}
