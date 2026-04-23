package middleware

import (
	"context"
	"testing"
	"time"

	"github.com/go-coldbrew/workers"
	"github.com/stretchr/testify/assert"
)

func TestDuration_ObservesNameAndDuration(t *testing.T) {
	var gotName string
	var gotDuration time.Duration

	mw := Duration(func(name string, d time.Duration) {
		gotName = name
		gotDuration = d
	})

	inner := workers.CycleFunc(func(ctx context.Context, info *workers.WorkerInfo) error {
		time.Sleep(10 * time.Millisecond)
		return nil
	})

	info := &workers.WorkerInfo{Name: "timed"}
	err := mw(inner).RunCycle(context.Background(), info)
	assert.NoError(t, err)
	assert.Equal(t, "timed", gotName)
	assert.GreaterOrEqual(t, gotDuration, 5*time.Millisecond)
}

func TestDuration_NilObserve(t *testing.T) {
	mw := Duration(nil)

	inner := workers.CycleFunc(func(ctx context.Context, info *workers.WorkerInfo) error {
		return nil
	})

	err := mw(inner).RunCycle(context.Background(), &workers.WorkerInfo{Name: "nil"})
	assert.NoError(t, err)
}

func TestDuration_ObservesOnError(t *testing.T) {
	var gotDuration time.Duration

	mw := Duration(func(_ string, d time.Duration) {
		gotDuration = d
	})

	inner := workers.CycleFunc(func(ctx context.Context, info *workers.WorkerInfo) error {
		time.Sleep(10 * time.Millisecond)
		return assert.AnError
	})

	err := mw(inner).RunCycle(context.Background(), &workers.WorkerInfo{Name: "err"})
	assert.Error(t, err)
	assert.GreaterOrEqual(t, gotDuration, 5*time.Millisecond)
}
