package middleware

import (
	"context"
	"errors"
	"testing"

	"github.com/go-coldbrew/workers"
	"github.com/stretchr/testify/assert"
)

func TestTracing_Success(t *testing.T) {
	mw := Tracing()

	called := false
	inner := workers.CycleFunc(func(ctx context.Context, info *workers.WorkerInfo) error {
		called = true
		return nil
	})

	err := mw(inner).RunCycle(context.Background(), &workers.WorkerInfo{Name: "traced"})
	assert.NoError(t, err)
	assert.True(t, called)
}

func TestTracing_Error(t *testing.T) {
	mw := Tracing()

	inner := workers.CycleFunc(func(ctx context.Context, info *workers.WorkerInfo) error {
		return errors.New("cycle failed")
	})

	err := mw(inner).RunCycle(context.Background(), &workers.WorkerInfo{Name: "traced-err"})
	assert.EqualError(t, err, "cycle failed")
}
