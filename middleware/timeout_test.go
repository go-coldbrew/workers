package middleware

import (
	"context"
	"testing"
	"time"

	"github.com/go-coldbrew/workers"
	"github.com/stretchr/testify/assert"
)

func TestTimeout_FastCycle(t *testing.T) {
	mw := Timeout(100 * time.Millisecond)

	inner := workers.CycleFunc(func(ctx context.Context, info *workers.WorkerInfo) error {
		return nil // completes immediately
	})

	err := mw(inner).RunCycle(context.Background(), &workers.WorkerInfo{Name: "fast"})
	assert.NoError(t, err)
}

func TestTimeout_SlowCycle(t *testing.T) {
	mw := Timeout(20 * time.Millisecond)

	inner := workers.CycleFunc(func(ctx context.Context, info *workers.WorkerInfo) error {
		<-ctx.Done()
		return ctx.Err()
	})

	err := mw(inner).RunCycle(context.Background(), &workers.WorkerInfo{Name: "slow"})
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestTimeout_PropagatesError(t *testing.T) {
	mw := Timeout(100 * time.Millisecond)

	inner := workers.CycleFunc(func(ctx context.Context, info *workers.WorkerInfo) error {
		return assert.AnError
	})

	err := mw(inner).RunCycle(context.Background(), &workers.WorkerInfo{Name: "err"})
	assert.ErrorIs(t, err, assert.AnError)
}
