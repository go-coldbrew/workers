package middleware

import (
	"context"
	"testing"

	"github.com/go-coldbrew/workers"
	"github.com/stretchr/testify/assert"
)

func TestSlog_NoPanic(t *testing.T) {
	// Verify Slog() doesn't panic and passes through correctly.
	mw := Slog()

	called := false
	info := workers.NewWorkerInfo("logged", 1)
	err := mw(context.Background(), info, func(_ context.Context, _ *workers.WorkerInfo) error {
		called = true
		return nil
	})

	assert.NoError(t, err)
	assert.True(t, called)
}

func TestSlog_PropagatesError(t *testing.T) {
	mw := Slog()

	info := workers.NewWorkerInfo("logged", 0)
	err := mw(context.Background(), info, func(_ context.Context, _ *workers.WorkerInfo) error {
		return assert.AnError
	})

	assert.ErrorIs(t, err, assert.AnError)
}
