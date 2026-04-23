package middleware

import (
	"context"
	"errors"
	"testing"

	"github.com/go-coldbrew/workers"
	"github.com/stretchr/testify/assert"
)

func TestSlog_Success(t *testing.T) {
	mw := Slog()

	called := false
	inner := workers.CycleFunc(func(ctx context.Context, info *workers.WorkerInfo) error {
		called = true
		return nil
	})

	err := mw(inner).RunCycle(context.Background(), &workers.WorkerInfo{Name: "slog-ok"})
	assert.NoError(t, err)
	assert.True(t, called)
}

func TestSlog_Error(t *testing.T) {
	mw := Slog()

	inner := workers.CycleFunc(func(ctx context.Context, info *workers.WorkerInfo) error {
		return errors.New("cycle error")
	})

	err := mw(inner).RunCycle(context.Background(), &workers.WorkerInfo{Name: "slog-err"})
	assert.EqualError(t, err, "cycle error")
}
