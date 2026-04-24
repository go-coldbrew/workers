package middleware

import (
	"context"
	"testing"

	"github.com/go-coldbrew/workers"
	"github.com/stretchr/testify/assert"
)

func TestTracing_NoPanic(t *testing.T) {
	// Verify Tracing() doesn't panic and passes through correctly.
	// Full span verification would require a tracing test harness.
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
