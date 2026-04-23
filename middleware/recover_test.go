package middleware

import (
	"context"
	"testing"

	"github.com/go-coldbrew/workers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRecover_CatchesPanic(t *testing.T) {
	var gotName string
	var gotValue any

	mw := Recover(func(name string, v any) {
		gotName = name
		gotValue = v
	})

	inner := workers.CycleFunc(func(ctx context.Context, info *workers.WorkerInfo) error {
		panic("boom")
	})

	info := &workers.WorkerInfo{Name: "panicker"}
	err := mw(inner).RunCycle(context.Background(), info)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "panicked")
	assert.Contains(t, err.Error(), "boom")
	assert.Equal(t, "panicker", gotName)
	assert.Equal(t, "boom", gotValue)
}

func TestRecover_NoPanic(t *testing.T) {
	mw := Recover(func(name string, v any) {
		t.Fatal("onPanic should not be called")
	})

	inner := workers.CycleFunc(func(ctx context.Context, info *workers.WorkerInfo) error {
		return nil
	})

	err := mw(inner).RunCycle(context.Background(), &workers.WorkerInfo{Name: "ok"})
	assert.NoError(t, err)
}

func TestRecover_NilCallback(t *testing.T) {
	mw := Recover(nil)

	inner := workers.CycleFunc(func(ctx context.Context, info *workers.WorkerInfo) error {
		panic("no callback")
	})

	err := mw(inner).RunCycle(context.Background(), &workers.WorkerInfo{Name: "nil-cb"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "panicked")
}

func TestRecover_DoesNotPropagate(t *testing.T) {
	mw := Recover(func(string, any) {})

	inner := workers.CycleFunc(func(ctx context.Context, info *workers.WorkerInfo) error {
		panic("should not propagate")
	})

	assert.NotPanics(t, func() {
		_ = mw(inner).RunCycle(context.Background(), &workers.WorkerInfo{Name: "safe"})
	})
}
