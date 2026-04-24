package middleware

import (
	"context"
	"testing"
	"time"

	"github.com/go-coldbrew/workers"
	"github.com/stretchr/testify/assert"
)

func TestTimeout_CancelsContext(t *testing.T) {
	mw := Timeout(20 * time.Millisecond)

	info := workers.NewWorkerInfo("slow", 0)
	err := mw(context.Background(), info, func(ctx context.Context, _ *workers.WorkerInfo) error {
		<-ctx.Done()
		return ctx.Err()
	})

	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestTimeout_FastCompletion(t *testing.T) {
	mw := Timeout(time.Second)

	info := workers.NewWorkerInfo("fast", 0)
	err := mw(context.Background(), info, func(_ context.Context, _ *workers.WorkerInfo) error {
		return nil
	})

	assert.NoError(t, err)
}
