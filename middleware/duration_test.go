package middleware

import (
	"context"
	"testing"
	"time"

	"github.com/go-coldbrew/workers"
	"github.com/stretchr/testify/assert"
)

func TestDuration_CallsObserve(t *testing.T) {
	var gotName string
	var gotDuration time.Duration

	mw := Duration(func(name string, d time.Duration) {
		gotName = name
		gotDuration = d
	})

	info := workers.NewWorkerInfo("timed", 0)
	err := mw(context.Background(), info, func(_ context.Context, _ *workers.WorkerInfo) error {
		time.Sleep(10 * time.Millisecond)
		return nil
	})

	assert.NoError(t, err)
	assert.Equal(t, "timed", gotName)
	assert.GreaterOrEqual(t, gotDuration, 10*time.Millisecond)
}

func TestDuration_PropagatesError(t *testing.T) {
	var called bool

	mw := Duration(func(_ string, _ time.Duration) {
		called = true
	})

	info := workers.NewWorkerInfo("timed", 0)
	err := mw(context.Background(), info, func(_ context.Context, _ *workers.WorkerInfo) error {
		return assert.AnError
	})

	assert.ErrorIs(t, err, assert.AnError)
	assert.True(t, called, "observe should be called even on error")
}
