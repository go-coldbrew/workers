package workers

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFromContext_Empty(t *testing.T) {
	info := FromContext(context.Background())
	assert.Equal(t, WorkerInfo{}, info)
}

func TestFromContext_Populated(t *testing.T) {
	ctx := WithWorkerInfo(context.Background(), WorkerInfo{Name: "test-worker", Attempt: 3})
	info := FromContext(ctx)
	assert.Equal(t, "test-worker", info.Name)
	assert.Equal(t, 3, info.Attempt)
}

func TestBuildChain_NoMiddleware(t *testing.T) {
	called := false
	original := func(ctx WorkerContext) error {
		called = true
		return nil
	}

	chain := buildChain(nil, nil, original)
	ctx := context.WithValue(context.Background(), wctxKey{},
		newWorkerContext(context.Background(), "test", 0, nil, nil, nil))
	err := chain.RunCycle(ctx, &WorkerInfo{Name: "test"})
	require.NoError(t, err)
	assert.True(t, called, "original function should be called")
}

func TestBuildChain_ExecutionOrder(t *testing.T) {
	var order []string

	mkMiddleware := func(name string) Middleware {
		return func(next CycleHandler) CycleHandler {
			return CycleFunc(func(ctx context.Context, info *WorkerInfo) error {
				order = append(order, name+":enter")
				err := next.RunCycle(ctx, info)
				order = append(order, name+":exit")
				return err
			})
		}
	}

	original := func(ctx WorkerContext) error {
		order = append(order, "fn")
		return nil
	}

	chain := buildChain(
		[]Middleware{mkMiddleware("A"), mkMiddleware("B")},
		[]Middleware{mkMiddleware("C")},
		original,
	)

	ctx := context.WithValue(context.Background(), wctxKey{},
		newWorkerContext(context.Background(), "test", 0, nil, nil, nil))
	err := chain.RunCycle(ctx, &WorkerInfo{Name: "test"})
	require.NoError(t, err)

	// Run-level [A, B] wraps outside worker-level [C].
	// Entry: A → B → C → fn; Exit: fn → C → B → A
	assert.Equal(t, []string{
		"A:enter", "B:enter", "C:enter", "fn", "C:exit", "B:exit", "A:exit",
	}, order)
}

func TestBuildChain_WorkerInfoExplicit(t *testing.T) {
	var gotName string
	var gotAttempt int

	mw := func(next CycleHandler) CycleHandler {
		return CycleFunc(func(ctx context.Context, info *WorkerInfo) error {
			gotName = info.Name
			gotAttempt = info.Attempt
			return next.RunCycle(ctx, info)
		})
	}

	original := func(ctx WorkerContext) error { return nil }

	chain := buildChain([]Middleware{mw}, nil, original)

	ctx := context.WithValue(context.Background(), wctxKey{},
		newWorkerContext(context.Background(), "my-worker", 2, nil, nil, nil))
	err := chain.RunCycle(ctx, &WorkerInfo{Name: "my-worker", Attempt: 2})
	require.NoError(t, err)

	assert.Equal(t, "my-worker", gotName)
	assert.Equal(t, 2, gotAttempt)
}

func TestBuildChain_ErrorPropagation(t *testing.T) {
	mw := func(next CycleHandler) CycleHandler {
		return CycleFunc(func(ctx context.Context, info *WorkerInfo) error {
			return next.RunCycle(ctx, info)
		})
	}

	original := func(ctx WorkerContext) error {
		return errors.New("test error")
	}

	chain := buildChain([]Middleware{mw}, nil, original)
	ctx := context.WithValue(context.Background(), wctxKey{},
		newWorkerContext(context.Background(), "test", 0, nil, nil, nil))
	err := chain.RunCycle(ctx, &WorkerInfo{Name: "test"})
	assert.EqualError(t, err, "test error")
}

func TestBuildChain_CloseNoError(t *testing.T) {
	// CycleFunc middleware has no-op Close. Verify chain.Close() works.
	original := func(ctx WorkerContext) error { return nil }
	chain := buildChain(
		[]Middleware{func(next CycleHandler) CycleHandler {
			return CycleFunc(func(ctx context.Context, info *WorkerInfo) error {
				return next.RunCycle(ctx, info)
			})
		}},
		nil,
		original,
	)
	err := chain.Close()
	assert.NoError(t, err)
}

func TestRun_WithMiddleware(t *testing.T) {
	var order []string

	mw := func(name string) Middleware {
		return func(next CycleHandler) CycleHandler {
			return CycleFunc(func(ctx context.Context, info *WorkerInfo) error {
				order = append(order, name+":enter")
				err := next.RunCycle(ctx, info)
				order = append(order, name+":exit")
				return err
			})
		}
	}

	w := NewWorker("mw-test", func(ctx WorkerContext) error {
		order = append(order, "fn")
		return nil
	}).Use(mw("worker"))

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	err := Run(ctx, []*Worker{w}, WithMiddleware(mw("run")))
	assert.NoError(t, err)

	// Run-level wraps outside worker-level.
	assert.Equal(t, []string{
		"run:enter", "worker:enter", "fn", "worker:exit", "run:exit",
	}, order)
}

func TestRun_WorkerInfoInMiddleware(t *testing.T) {
	var gotName string
	var gotAttempt int

	mw := func(next CycleHandler) CycleHandler {
		return CycleFunc(func(ctx context.Context, info *WorkerInfo) error {
			gotName = info.Name
			gotAttempt = info.Attempt
			return next.RunCycle(ctx, info)
		})
	}

	w := NewWorker("info-worker", func(ctx WorkerContext) error {
		return nil
	}).Use(mw)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	_ = Run(ctx, []*Worker{w})
	assert.Equal(t, "info-worker", gotName)
	assert.Equal(t, 0, gotAttempt)
}

func TestRun_MiddlewarePerTick(t *testing.T) {
	// Middleware should run on every tick for periodic workers.
	var mwCalls atomic.Int32

	mw := func(next CycleHandler) CycleHandler {
		return CycleFunc(func(ctx context.Context, info *WorkerInfo) error {
			mwCalls.Add(1)
			return next.RunCycle(ctx, info)
		})
	}

	w := NewWorker("per-tick", func(ctx WorkerContext) error {
		return nil
	}).Every(10 * time.Millisecond).Use(mw)

	ctx, cancel := context.WithTimeout(context.Background(), 55*time.Millisecond)
	defer cancel()

	_ = Run(ctx, []*Worker{w})
	assert.GreaterOrEqual(t, int(mwCalls.Load()), 3, "middleware should run per tick")
}

func TestRun_ChainCloseCalledOnStop(t *testing.T) {
	mw := func(next CycleHandler) CycleHandler {
		return CycleFunc(func(ctx context.Context, info *WorkerInfo) error {
			return next.RunCycle(ctx, info)
		})
	}

	w := NewWorker("close-test", func(ctx WorkerContext) error {
		return nil
	}).Use(mw)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := Run(ctx, []*Worker{w})
	assert.NoError(t, err)
}
