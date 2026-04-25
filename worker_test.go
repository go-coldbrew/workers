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
		assert.Equal(t, "test", info.GetName())
		assert.Equal(t, 0, info.GetAttempt())
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
	assert.Equal(t, "myworker", info.GetName())
	assert.Equal(t, 3, info.GetAttempt())
}

func TestWorkerInfo_Children_Nil(t *testing.T) {
	info := &WorkerInfo{name: "test"}
	assert.Empty(t, info.GetChildren(), "Children on nil sup should return empty slice")
}

func TestCycleFunc_Close(t *testing.T) {
	fn := CycleFunc(func(_ context.Context, _ *WorkerInfo) error { return nil })
	assert.NoError(t, fn.Close(), "CycleFunc.Close should be a no-op")
}

func TestWorker_GetName(t *testing.T) {
	w := NewWorker("my-worker").HandlerFunc(func(_ context.Context, _ *WorkerInfo) error { return nil })
	assert.Equal(t, "my-worker", w.GetName())
}

func TestWorker_GetHandler(t *testing.T) {
	fn := CycleFunc(func(_ context.Context, _ *WorkerInfo) error { return nil })
	w := NewWorker("test").HandlerFunc(fn)
	assert.NotNil(t, w.GetHandler())

	w2 := NewWorker("no-handler")
	assert.Nil(t, w2.GetHandler())
}

func TestWorker_WithFailureDecay(t *testing.T) {
	w := NewWorker("test").HandlerFunc(func(_ context.Context, _ *WorkerInfo) error { return nil })
	w.WithFailureDecay(2.0)
	assert.Equal(t, 2.0, w.failureDecay)
}

func TestWorker_WithFailureThreshold(t *testing.T) {
	w := NewWorker("test").HandlerFunc(func(_ context.Context, _ *WorkerInfo) error { return nil })
	w.WithFailureThreshold(10)
	assert.Equal(t, 10.0, w.failureThreshold)
}

func TestWorker_WithFailureBackoff(t *testing.T) {
	w := NewWorker("test").HandlerFunc(func(_ context.Context, _ *WorkerInfo) error { return nil })
	w.WithFailureBackoff(5 * time.Second)
	assert.Equal(t, 5*time.Second, w.failureBackoff)
}

func TestWorker_WithTimeout(t *testing.T) {
	w := NewWorker("test").HandlerFunc(func(_ context.Context, _ *WorkerInfo) error { return nil })
	w.WithTimeout(30 * time.Second)
	assert.Equal(t, 30*time.Second, w.timeout)
}

func TestWorker_WithBackoffJitter(t *testing.T) {
	w := NewWorker("test").HandlerFunc(func(_ context.Context, _ *WorkerInfo) error { return nil })
	w.WithBackoffJitter(func(d time.Duration) time.Duration { return d / 2 })
	assert.NotNil(t, w.backoffJitter)
}

func TestWorkerInfo_GetHandler(t *testing.T) {
	fn := CycleFunc(func(_ context.Context, _ *WorkerInfo) error { return nil })
	info := NewWorkerInfo("test", 0, WithTestHandler(fn))
	assert.NotNil(t, info.GetHandler())
}

func TestWorkerInfo_GetHandler_Nil(t *testing.T) {
	info := NewWorkerInfo("test", 0)
	assert.Nil(t, info.GetHandler())
}

func TestWorkerInfo_GetChild(t *testing.T) {
	info := NewWorkerInfo("parent", 0, WithTestChildren(t.Context()))

	// No child yet.
	_, ok := info.GetChild("nonexistent")
	assert.False(t, ok)

	// Add a child.
	info.Add(NewWorker("child").HandlerFunc(func(ctx context.Context, _ *WorkerInfo) error {
		<-ctx.Done()
		return ctx.Err()
	}))
	time.Sleep(20 * time.Millisecond) // let child start

	child, ok := info.GetChild("child")
	assert.True(t, ok)
	assert.Equal(t, "child", child.GetName())

	// Verify it's a copy — mutations don't affect the running worker.
	child.WithRestart(false)
	child2, _ := info.GetChild("child")
	assert.True(t, child2.restartOnFail, "mutation on copy should not affect original")
}

func TestWorkerInfo_Add_SkipsExisting(t *testing.T) {
	info := NewWorkerInfo("parent", 0, WithTestChildren(t.Context()))

	childFn := CycleFunc(func(ctx context.Context, _ *WorkerInfo) error {
		<-ctx.Done()
		return ctx.Err()
	})

	added := info.Add(NewWorker("child").HandlerFunc(childFn))
	assert.True(t, added, "first Add should succeed")
	time.Sleep(20 * time.Millisecond)

	// Second Add with same name is a no-op.
	added = info.Add(NewWorker("child").HandlerFunc(childFn))
	assert.False(t, added, "duplicate Add should be skipped")

	assert.Equal(t, []string{"child"}, info.GetChildren())
}

func TestWorkerInfo_Add_AfterRemove(t *testing.T) {
	info := NewWorkerInfo("parent", 0, WithTestChildren(t.Context()))

	childFn := CycleFunc(func(ctx context.Context, _ *WorkerInfo) error {
		<-ctx.Done()
		return ctx.Err()
	})

	info.Add(NewWorker("child").HandlerFunc(childFn))
	time.Sleep(20 * time.Millisecond)

	info.Remove("child")
	time.Sleep(20 * time.Millisecond)

	// After Remove, Add should succeed again.
	added := info.Add(NewWorker("child").HandlerFunc(childFn))
	assert.True(t, added, "Add after Remove should succeed")
}

func TestNewWorkerInfo_WithTestChildren(t *testing.T) {
	info := NewWorkerInfo("test-parent", 0, WithTestChildren(t.Context()))

	// Verify Add/Remove/GetChildren work.
	info.Add(NewWorker("a").HandlerFunc(func(ctx context.Context, _ *WorkerInfo) error {
		<-ctx.Done()
		return ctx.Err()
	}))
	info.Add(NewWorker("b").HandlerFunc(func(ctx context.Context, _ *WorkerInfo) error {
		<-ctx.Done()
		return ctx.Err()
	}))
	time.Sleep(20 * time.Millisecond)

	assert.Equal(t, []string{"a", "b"}, info.GetChildren())

	info.Remove("a")
	time.Sleep(20 * time.Millisecond)

	assert.Equal(t, []string{"b"}, info.GetChildren())
}

func TestWorkerInfo_ZombieChild_AutoCleanup(t *testing.T) {
	info := NewWorkerInfo("parent", 0, WithTestChildren(t.Context()))

	// Add a child that returns nil immediately (no restart).
	info.Add(NewWorker("child").HandlerFunc(func(_ context.Context, _ *WorkerInfo) error {
		return nil
	}).WithRestart(false))

	time.Sleep(100 * time.Millisecond) // let child stop

	// Suture removes the stopped service — GetChildCount reflects this.
	assert.Equal(t, 0, len(info.GetChildren()))
	assert.Empty(t, info.GetChildren())
}

func TestWorkerInfo_ZombieChild_ErrDoNotRestart(t *testing.T) {
	info := NewWorkerInfo("parent", 0, WithTestChildren(t.Context()))

	info.Add(NewWorker("child").HandlerFunc(func(_ context.Context, _ *WorkerInfo) error {
		return ErrDoNotRestart
	}))

	time.Sleep(100 * time.Millisecond)

	assert.Equal(t, 0, len(info.GetChildren()))
	assert.Empty(t, info.GetChildren())
}

func TestWorkerInfo_ZombieChild_ReAdd(t *testing.T) {
	info := NewWorkerInfo("parent", 0, WithTestChildren(t.Context()))

	// Add a child that stops immediately.
	info.Add(NewWorker("child").HandlerFunc(func(_ context.Context, _ *WorkerInfo) error {
		return nil
	}).WithRestart(false))

	time.Sleep(100 * time.Millisecond)

	// Suture removed the stopped child — Add with same name should succeed.
	added := info.Add(NewWorker("child").HandlerFunc(func(ctx context.Context, _ *WorkerInfo) error {
		<-ctx.Done()
		return ctx.Err()
	}))
	assert.True(t, added, "re-Add after zombie prune should succeed")
	time.Sleep(20 * time.Millisecond)
	assert.Equal(t, 1, len(info.GetChildren()))
}

func TestWorkerInfo_ZombieChild_ReAdd_NoRead(t *testing.T) {
	info := NewWorkerInfo("parent", 0, WithTestChildren(t.Context()))

	// Add a child that stops immediately.
	info.Add(NewWorker("child").HandlerFunc(func(_ context.Context, _ *WorkerInfo) error {
		return nil
	}).WithRestart(false))

	time.Sleep(100 * time.Millisecond)

	// Re-Add directly — suture already removed the stopped service,
	// so Add sees no conflict via Services().
	added := info.Add(NewWorker("child").HandlerFunc(func(ctx context.Context, _ *WorkerInfo) error {
		<-ctx.Done()
		return ctx.Err()
	}))
	assert.True(t, added, "Add should succeed after stopped child is gone from suture")
	time.Sleep(20 * time.Millisecond)
	assert.Equal(t, 1, len(info.GetChildren()))
}

func TestWorkerInfo_ZombieChild_GetChild(t *testing.T) {
	info := NewWorkerInfo("parent", 0, WithTestChildren(t.Context()))

	info.Add(NewWorker("child").HandlerFunc(func(_ context.Context, _ *WorkerInfo) error {
		return nil
	}).WithRestart(false))

	time.Sleep(100 * time.Millisecond)

	// GetChild queries suture — stopped child is not found.
	_, ok := info.GetChild("child")
	assert.False(t, ok)
}

func TestNewWorkerInfo_Minimal(t *testing.T) {
	// Without options, Add/Remove/GetChildren are safe no-ops.
	info := NewWorkerInfo("test", 5)
	assert.Equal(t, "test", info.GetName())
	assert.Equal(t, 5, info.GetAttempt())
	assert.Empty(t, info.GetChildren())

	// Add on nil sup is a no-op.
	info.Add(NewWorker("child").HandlerFunc(func(_ context.Context, _ *WorkerInfo) error { return nil }))
	assert.Empty(t, info.GetChildren())
}

func TestWorker_ConfigGetters(t *testing.T) {
	w := NewWorker("test").
		HandlerFunc(func(_ context.Context, _ *WorkerInfo) error { return nil }).
		Every(30 * time.Second).
		WithJitter(15).
		WithInitialDelay(5 * time.Second).
		WithRestart(false)

	assert.Equal(t, 30*time.Second, w.GetInterval())
	assert.Equal(t, 15, w.GetJitterPercent())
	assert.Equal(t, 5*time.Second, w.GetInitialDelay())
	assert.False(t, w.GetRestartOnFail())
}

func TestWorker_ConfigGetters_Defaults(t *testing.T) {
	w := NewWorker("test")

	assert.Equal(t, time.Duration(0), w.GetInterval())
	assert.Equal(t, -1, w.GetJitterPercent())
	assert.Equal(t, time.Duration(0), w.GetInitialDelay())
	assert.True(t, w.GetRestartOnFail())
}

func TestWorker_InterceptorsCopiesSlice(t *testing.T) {
	mw := func(_ context.Context, _ *WorkerInfo, next CycleFunc) error { return next(nil, nil) }
	original := []Middleware{mw}

	w := NewWorker("test").HandlerFunc(func(_ context.Context, _ *WorkerInfo) error { return nil })
	w.Interceptors(original...)

	// Mutate the original slice — should not affect the worker.
	original[0] = nil
	assert.NotNil(t, w.interceptors[0], "Interceptors should copy the slice")
}
