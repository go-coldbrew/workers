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
	w := NewWorker("test").HandlerFunc(func(_ context.Context, info *WorkerInfo) error {
		called = true
		assert.Equal(t, "test", info.Name())
		assert.Equal(t, 0, info.Attempt())
		return nil
	})
	require.NotNil(t, w)
	assert.Equal(t, "test", w.name)
	assert.True(t, w.restartOnFail, "restart should be true by default")

	// Run the handler directly to verify.
	info := &WorkerInfo{name: "test", attempt: 0}
	err := w.handler.RunCycle(context.Background(), info)
	assert.NoError(t, err)
	assert.True(t, called)
}

func TestWorker_WithRestart(t *testing.T) {
	w := NewWorker("test").HandlerFunc(func(_ context.Context, _ *WorkerInfo) error { return nil })
	assert.True(t, w.restartOnFail, "default should be true")

	w.WithRestart(false)
	assert.False(t, w.restartOnFail)
}

func TestWorker_Every(t *testing.T) {
	w := NewWorker("ticker").HandlerFunc(func(_ context.Context, _ *WorkerInfo) error {
		return nil
	}).Every(10 * time.Millisecond)

	assert.Equal(t, 10*time.Millisecond, w.interval, "Every should store interval as data")
	assert.NotNil(t, w.handler, "Every should NOT replace the handler (wrapping is deferred)")
}

func TestWorker_WithJitter(t *testing.T) {
	w := NewWorker("test").HandlerFunc(func(_ context.Context, _ *WorkerInfo) error { return nil })
	assert.Equal(t, -1, w.jitterPercent, "default jitter should be -1 (inherit)")

	w.WithJitter(10)
	assert.Equal(t, 10, w.jitterPercent)

	w.WithJitter(0)
	assert.Equal(t, 0, w.jitterPercent, "0 explicitly disables jitter")
}

func TestWorker_WithInitialDelay(t *testing.T) {
	w := NewWorker("test").HandlerFunc(func(_ context.Context, _ *WorkerInfo) error { return nil })
	w.WithInitialDelay(5 * time.Second)
	assert.Equal(t, 5*time.Second, w.initialDelay)
}

func TestWorker_Interceptors(t *testing.T) {
	mw1 := func(_ context.Context, _ *WorkerInfo, next CycleFunc) error { return next(nil, nil) }
	mw2 := func(_ context.Context, _ *WorkerInfo, next CycleFunc) error { return next(nil, nil) }

	w := NewWorker("test").HandlerFunc(func(_ context.Context, _ *WorkerInfo) error { return nil })
	assert.Empty(t, w.interceptors)

	w.Interceptors(mw1)
	assert.Len(t, w.interceptors, 1)

	// Interceptors replaces.
	w.Interceptors(mw2)
	assert.Len(t, w.interceptors, 1)

	// AddInterceptors appends.
	w.AddInterceptors(mw1)
	assert.Len(t, w.interceptors, 2)
}

func TestWorkerInfo(t *testing.T) {
	info := &WorkerInfo{name: "myworker", attempt: 3}
	assert.Equal(t, "myworker", info.Name())
	assert.Equal(t, 3, info.Attempt())
}

func TestWorkerInfo_Children_Nil(t *testing.T) {
	info := &WorkerInfo{name: "test"}
	assert.Empty(t, info.Children(), "Children on nil map should return empty slice")
}

func TestCycleFunc_Close(t *testing.T) {
	fn := CycleFunc(func(_ context.Context, _ *WorkerInfo) error { return nil })
	assert.NoError(t, fn.Close(), "CycleFunc.Close should be a no-op")
}
