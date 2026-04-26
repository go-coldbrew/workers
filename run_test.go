package workers

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestRun_BasicLifecycle(t *testing.T) {
	var started atomic.Bool
	w := NewWorker("basic").HandlerFunc(func(ctx context.Context, _ *WorkerInfo) error {
		started.Store(true)
		<-ctx.Done()
		return ctx.Err()
	})

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := Run(ctx, []*Worker{w})
	assert.NoError(t, err)
	assert.True(t, started.Load())
}

func TestRun_MultipleWorkers(t *testing.T) {
	var count atomic.Int32
	mkWorker := func(name string) *Worker {
		return NewWorker(name).HandlerFunc(func(ctx context.Context, _ *WorkerInfo) error {
			count.Add(1)
			<-ctx.Done()
			return ctx.Err()
		})
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := Run(ctx, []*Worker{mkWorker("a"), mkWorker("b"), mkWorker("c")})
	assert.NoError(t, err)
	assert.Equal(t, int32(3), count.Load())
}

func TestRun_WorkerPanicRecovery(t *testing.T) {
	var attempts atomic.Int32
	w := NewWorker("panicker").HandlerFunc(func(ctx context.Context, _ *WorkerInfo) error {
		a := attempts.Add(1)
		if a == 1 {
			panic("boom")
		}
		<-ctx.Done()
		return ctx.Err()
	}).WithRestart(true)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	err := Run(ctx, []*Worker{w})
	assert.NoError(t, err)
	assert.GreaterOrEqual(t, int(attempts.Load()), 2, "should restart after panic")
}

func TestRun_RestartOnFail(t *testing.T) {
	var attempts atomic.Int32
	w := NewWorker("failer").HandlerFunc(func(ctx context.Context, _ *WorkerInfo) error {
		a := attempts.Add(1)
		if a <= 2 {
			return errors.New("transient error")
		}
		<-ctx.Done()
		return ctx.Err()
	}).WithRestart(true)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := Run(ctx, []*Worker{w})
	assert.NoError(t, err)
	assert.GreaterOrEqual(t, int(attempts.Load()), 3, "should restart at least twice")
}

func TestRun_NoRestartOnFail(t *testing.T) {
	var attempts atomic.Int32
	w := NewWorker("oneshot").HandlerFunc(func(_ context.Context, _ *WorkerInfo) error {
		attempts.Add(1)
		return nil // exits cleanly
	}).WithRestart(false)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	err := Run(ctx, []*Worker{w})
	assert.NoError(t, err)
	assert.Equal(t, int32(1), attempts.Load(), "should not restart")
}

func TestRun_WrappedErrDoNotRestart(t *testing.T) {
	var attempts atomic.Int32
	w := NewWorker("wrapped-stop").HandlerFunc(func(_ context.Context, _ *WorkerInfo) error {
		attempts.Add(1)
		return fmt.Errorf("work done: %w", ErrDoNotRestart)
	})

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	Run(ctx, []*Worker{w})
	assert.Equal(t, int32(1), attempts.Load(), "wrapped ErrDoNotRestart should prevent restart")
}

func TestRun_WorkerInfoName(t *testing.T) {
	var gotName string
	w := NewWorker("named-worker").HandlerFunc(func(_ context.Context, info *WorkerInfo) error {
		gotName = info.GetName()
		return nil
	}).WithRestart(false)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	_ = Run(ctx, []*Worker{w})
	assert.Equal(t, "named-worker", gotName)
}

func TestRun_WorkerInfoAttempt(t *testing.T) {
	var mu sync.Mutex
	var attempts []int
	w := NewWorker("attempt-tracker").HandlerFunc(func(ctx context.Context, info *WorkerInfo) error {
		mu.Lock()
		attempts = append(attempts, info.GetAttempt())
		mu.Unlock()
		if len(attempts) < 3 {
			return errors.New("fail")
		}
		<-ctx.Done()
		return ctx.Err()
	}).WithRestart(true)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_ = Run(ctx, []*Worker{w})
	mu.Lock()
	defer mu.Unlock()
	assert.GreaterOrEqual(t, len(attempts), 3)
	assert.Equal(t, 0, attempts[0])
	assert.Equal(t, 1, attempts[1])
	assert.Equal(t, 2, attempts[2])
}

func TestRunWorker_Single(t *testing.T) {
	var started atomic.Bool
	w := NewWorker("single").HandlerFunc(func(ctx context.Context, _ *WorkerInfo) error {
		started.Store(true)
		<-ctx.Done()
		return ctx.Err()
	})

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	RunWorker(ctx, w)
	assert.True(t, started.Load())
}

func TestRun_WithInterceptors(t *testing.T) {
	var order []string
	var mu sync.Mutex

	mw := func(tag string) Middleware {
		return func(ctx context.Context, info *WorkerInfo, next CycleFunc) error {
			mu.Lock()
			order = append(order, tag+":before")
			mu.Unlock()
			err := next(ctx, info)
			mu.Lock()
			order = append(order, tag+":after")
			mu.Unlock()
			return err
		}
	}

	w := NewWorker("test").HandlerFunc(func(_ context.Context, _ *WorkerInfo) error {
		mu.Lock()
		order = append(order, "handler")
		mu.Unlock()
		return nil
	}).WithRestart(false)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	Run(ctx, []*Worker{w}, WithInterceptors(mw("run")))

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, []string{"run:before", "handler", "run:after"}, order)
}

func TestRun_MiddlewareOrdering(t *testing.T) {
	var order []string
	var mu sync.Mutex

	mw := func(tag string) Middleware {
		return func(ctx context.Context, info *WorkerInfo, next CycleFunc) error {
			mu.Lock()
			order = append(order, tag)
			mu.Unlock()
			return next(ctx, info)
		}
	}

	w := NewWorker("test").
		HandlerFunc(func(_ context.Context, _ *WorkerInfo) error {
			mu.Lock()
			order = append(order, "handler")
			mu.Unlock()
			return nil
		}).
		Interceptors(mw("worker-mw")).
		WithRestart(false)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	Run(ctx, []*Worker{w}, WithInterceptors(mw("run-mw")))

	mu.Lock()
	defer mu.Unlock()
	// Run-level wraps outside worker-level.
	assert.Equal(t, []string{"run-mw", "worker-mw", "handler"}, order)
}

func TestRun_HandlerClose(t *testing.T) {
	var closeCount atomic.Int32

	handler := &closableHandler{
		runCycle: func(ctx context.Context, _ *WorkerInfo) error {
			<-ctx.Done()
			return ctx.Err()
		},
		close: func() error {
			closeCount.Add(1)
			return nil
		},
	}

	w := NewWorker("test").Handler(handler)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	Run(ctx, []*Worker{w})
	assert.Equal(t, int32(1), closeCount.Load(), "Close() should be called exactly once on shutdown")
}

func TestRun_HandlerClose_NotCalledOnRestart(t *testing.T) {
	var closeCount atomic.Int32
	var attempts atomic.Int32

	handler := &closableHandler{
		runCycle: func(ctx context.Context, _ *WorkerInfo) error {
			if attempts.Add(1) <= 2 {
				return errors.New("transient")
			}
			<-ctx.Done()
			return ctx.Err()
		},
		close: func() error {
			closeCount.Add(1)
			return nil
		},
	}

	w := NewWorker("test").Handler(handler).WithRestart(true)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	Run(ctx, []*Worker{w})
	assert.GreaterOrEqual(t, int(attempts.Load()), 3, "should have restarted")
	assert.Equal(t, int32(1), closeCount.Load(), "Close() should be called once, not per restart")
}

type closableHandler struct {
	runCycle func(ctx context.Context, info *WorkerInfo) error
	close    func() error
}

func (h *closableHandler) RunCycle(ctx context.Context, info *WorkerInfo) error {
	return h.runCycle(ctx, info)
}

func (h *closableHandler) Close() error {
	return h.close()
}

func TestRun_NilHandler(t *testing.T) {
	// Worker with no handler should not panic — uses default (block until ctx done).
	w := NewWorker("nil-handler").WithRestart(false)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := Run(ctx, []*Worker{w})
	assert.NoError(t, err)
}

func TestBuildChain(t *testing.T) {
	var order []string

	mw1 := Middleware(func(ctx context.Context, info *WorkerInfo, next CycleFunc) error {
		order = append(order, "mw1:before")
		err := next(ctx, info)
		order = append(order, "mw1:after")
		return err
	})
	mw2 := Middleware(func(ctx context.Context, info *WorkerInfo, next CycleFunc) error {
		order = append(order, "mw2:before")
		err := next(ctx, info)
		order = append(order, "mw2:after")
		return err
	})

	handler := CycleFunc(func(_ context.Context, _ *WorkerInfo) error {
		order = append(order, "handler")
		return nil
	})

	chain := buildChain([]Middleware{mw1, mw2}, handler)
	err := chain(context.Background(), &WorkerInfo{name: "test"})
	assert.NoError(t, err)
	assert.Equal(t, []string{"mw1:before", "mw2:before", "handler", "mw2:after", "mw1:after"}, order)
}

func TestBuildChain_Empty(t *testing.T) {
	called := false
	handler := CycleFunc(func(_ context.Context, _ *WorkerInfo) error {
		called = true
		return nil
	})

	chain := buildChain(nil, handler)
	err := chain(context.Background(), &WorkerInfo{name: "test"})
	assert.NoError(t, err)
	assert.True(t, called)
}

func TestRun_WithDefaultJitter(t *testing.T) {
	// Verify that a periodic worker picks up run-level default jitter.
	// We can't easily assert jitter randomness in a unit test, but we can
	// verify the worker runs successfully with jitter enabled.
	var count atomic.Int32
	w := NewWorker("jittery").HandlerFunc(func(_ context.Context, _ *WorkerInfo) error {
		count.Add(1)
		return nil
	}).Every(10 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()

	Run(ctx, []*Worker{w}, WithDefaultJitter(20))
	assert.GreaterOrEqual(t, int(count.Load()), 2, "should tick multiple times with jitter")
}

func TestRun_AddInterceptors(t *testing.T) {
	var order []string
	var mu sync.Mutex

	mw := func(tag string) Middleware {
		return func(ctx context.Context, info *WorkerInfo, next CycleFunc) error {
			mu.Lock()
			order = append(order, tag)
			mu.Unlock()
			return next(ctx, info)
		}
	}

	w := NewWorker("test").HandlerFunc(func(_ context.Context, _ *WorkerInfo) error {
		mu.Lock()
		order = append(order, "handler")
		mu.Unlock()
		return nil
	}).WithRestart(false)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	Run(ctx, []*Worker{w},
		WithInterceptors(mw("base")),
		AddInterceptors(mw("extra")),
	)

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, []string{"base", "extra", "handler"}, order)
}

func TestRun_ClosingSupervisor_ClosesOnShutdown(t *testing.T) {
	var closeCount atomic.Int32
	handler := &closableHandler{
		runCycle: func(ctx context.Context, _ *WorkerInfo) error {
			<-ctx.Done()
			return ctx.Err()
		},
		close: func() error {
			closeCount.Add(1)
			return nil
		},
	}

	w := NewWorker("test").Handler(handler)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	Run(ctx, []*Worker{w})
	assert.Equal(t, int32(1), closeCount.Load(), "Close should be called exactly once")
}

func TestRun_ErrSkipTick_PeriodicWorker(t *testing.T) {
	var count atomic.Int32
	w := NewWorker("skipper").HandlerFunc(func(_ context.Context, _ *WorkerInfo) error {
		n := count.Add(1)
		if n == 1 {
			return ErrSkipTick
		}
		return nil
	}).Every(10 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()

	err := Run(ctx, []*Worker{w})
	assert.NoError(t, err)
	assert.GreaterOrEqual(t, int(count.Load()), 2, "should tick again after ErrSkipTick")
}

func TestRun_ErrSkipTick_NotCountedAsFailure(t *testing.T) {
	m := newMockMetrics()
	w := NewWorker("skipper").HandlerFunc(func(_ context.Context, _ *WorkerInfo) error {
		return ErrSkipTick
	}).Every(10 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	Run(ctx, []*Worker{w}, WithMetrics(m))

	m.mu.Lock()
	defer m.mu.Unlock()
	assert.Empty(t, m.failed, "ErrSkipTick should not be counted as failure")
}

func TestRun_ErrSkipTick_NonPeriodic_CountedAsFailure(t *testing.T) {
	m := newMockMetrics()
	var attempts atomic.Int32
	w := NewWorker("non-periodic-skipper").HandlerFunc(func(_ context.Context, _ *WorkerInfo) error {
		attempts.Add(1)
		return ErrSkipTick // meaningless for non-periodic, should be treated as normal error
	}).WithRestart(false) // stop after first attempt

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	Run(ctx, []*Worker{w}, WithMetrics(m))

	m.mu.Lock()
	defer m.mu.Unlock()
	assert.NotEmpty(t, m.failed, "ErrSkipTick from non-periodic worker should be counted as failure")
}

func TestRun_ErrDoNotRestart_NotCountedAsFailure(t *testing.T) {
	m := newMockMetrics()
	w := NewWorker("completer").HandlerFunc(func(_ context.Context, _ *WorkerInfo) error {
		return ErrDoNotRestart
	})

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	Run(ctx, []*Worker{w}, WithMetrics(m))

	m.mu.Lock()
	defer m.mu.Unlock()
	assert.Empty(t, m.failed, "ErrDoNotRestart should not be counted as failure")
}

func TestRun_ResolveMetrics_DefaultFallback(t *testing.T) {
	// Worker with no metrics, no parent metrics — should use BaseMetrics.
	w := NewWorker("test").HandlerFunc(func(ctx context.Context, _ *WorkerInfo) error {
		<-ctx.Done()
		return ctx.Err()
	})

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	// Should not panic.
	Run(ctx, []*Worker{w})
}
