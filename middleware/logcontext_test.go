package middleware

import (
	"context"
	"testing"

	"github.com/go-coldbrew/workers"
	"github.com/stretchr/testify/assert"
)

func TestLogContext_NoPanic(t *testing.T) {
	mw := LogContext()

	called := false
	info := workers.NewWorkerInfo("ctx-enriched", 2)
	err := mw(context.Background(), info, func(_ context.Context, _ *workers.WorkerInfo) error {
		called = true
		return nil
	})

	assert.NoError(t, err)
	assert.True(t, called)
}

func TestLogContext_PropagatesError(t *testing.T) {
	mw := LogContext()

	info := workers.NewWorkerInfo("ctx-enriched", 0)
	err := mw(context.Background(), info, func(_ context.Context, _ *workers.WorkerInfo) error {
		return assert.AnError
	})

	assert.ErrorIs(t, err, assert.AnError)
}
