package middleware

import (
	"context"
	"testing"

	"github.com/go-coldbrew/workers"
	"github.com/stretchr/testify/assert"
)

func TestRecover_CatchesPanic(t *testing.T) {
	var gotName string
	var gotValue any

	mw := Recover(func(name string, v any) {
		gotName = name
		gotValue = v
	})

	info := workers.NewWorkerInfo("panicker", 0)
	err := mw(context.Background(), info, func(_ context.Context, _ *workers.WorkerInfo) error {
		panic("boom")
	})

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "panic in worker panicker: boom")
	assert.Equal(t, "panicker", gotName)
	assert.Equal(t, "boom", gotValue)
}

func TestRecover_NilCallback(t *testing.T) {
	mw := Recover(nil)

	info := workers.NewWorkerInfo("panicker", 0)
	err := mw(context.Background(), info, func(_ context.Context, _ *workers.WorkerInfo) error {
		panic("boom")
	})

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "panic in worker panicker: boom")
}

func TestRecover_NoPanic(t *testing.T) {
	called := false
	mw := Recover(func(_ string, _ any) {
		called = true
	})

	info := workers.NewWorkerInfo("safe", 0)
	err := mw(context.Background(), info, func(_ context.Context, _ *workers.WorkerInfo) error {
		return nil
	})

	assert.NoError(t, err)
	assert.False(t, called, "callback should not be called when no panic")
}

func TestRecover_PassesError(t *testing.T) {
	mw := Recover(nil)

	info := workers.NewWorkerInfo("errer", 0)
	err := mw(context.Background(), info, func(_ context.Context, _ *workers.WorkerInfo) error {
		return assert.AnError
	})

	assert.ErrorIs(t, err, assert.AnError, "non-panic errors should pass through")
}
