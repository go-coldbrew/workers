package workers

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewWorker(t *testing.T) {
	called := false
	w := NewWorker("test", func(ctx WorkerContext) error {
		called = true
		assert.Equal(t, "test", ctx.Name())
		assert.Equal(t, 0, ctx.Attempt())
		return nil
	})
	require.NotNil(t, w)
	assert.Equal(t, "test", w.name)
	assert.False(t, w.restartOnFail)

	// Run it directly to verify
	wctx := newWorkerContext(context.Background(), "test", 0, nil, nil, nil)
	err := w.run(wctx)
	assert.NoError(t, err)
	assert.True(t, called)
}

func TestWorker_WithRestart(t *testing.T) {
	w := NewWorker("test", func(ctx WorkerContext) error { return nil })
	assert.False(t, w.restartOnFail)

	w.WithRestart(true)
	assert.True(t, w.restartOnFail)
}

func TestWorker_Every(t *testing.T) {
	count := 0
	w := NewWorker("ticker", func(ctx WorkerContext) error {
		count++
		return nil
	}).Every(10 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 55*time.Millisecond)
	defer cancel()

	// Every() stores the interval; the ticker loop is built by Run.
	_ = Run(ctx, []*Worker{w})
	assert.GreaterOrEqual(t, count, 3, "should tick at least 3 times in 55ms with 10ms interval")
}

func TestWorkerContext(t *testing.T) {
	ctx := context.WithValue(context.Background(), "key", "value")
	wctx := newWorkerContext(ctx, "myworker", 3, nil, nil, nil)

	assert.Equal(t, "myworker", wctx.Name())
	assert.Equal(t, 3, wctx.Attempt())
	assert.Equal(t, "value", wctx.Value("key"))
}
