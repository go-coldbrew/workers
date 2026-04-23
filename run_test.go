package workers

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestRun_BasicLifecycle(t *testing.T) {
	var started atomic.Bool
	w := NewWorker("basic", func(ctx WorkerContext) error {
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
		return NewWorker(name, func(ctx WorkerContext) error {
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
	w := NewWorker("panicker", func(ctx WorkerContext) error {
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
	w := NewWorker("failer", func(ctx WorkerContext) error {
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
	w := NewWorker("oneshot", func(ctx WorkerContext) error {
		attempts.Add(1)
		return nil // exits cleanly
	})

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	err := Run(ctx, []*Worker{w})
	assert.NoError(t, err)
	assert.Equal(t, int32(1), attempts.Load(), "should not restart")
}

func TestRun_WorkerContextName(t *testing.T) {
	var gotName string
	w := NewWorker("named-worker", func(ctx WorkerContext) error {
		gotName = ctx.Name()
		return nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	_ = Run(ctx, []*Worker{w})
	assert.Equal(t, "named-worker", gotName)
}

func TestRun_WorkerContextAttempt(t *testing.T) {
	var attempts []int
	w := NewWorker("attempt-tracker", func(ctx WorkerContext) error {
		attempts = append(attempts, ctx.Attempt())
		if len(attempts) < 3 {
			return errors.New("fail")
		}
		<-ctx.Done()
		return ctx.Err()
	}).WithRestart(true)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_ = Run(ctx, []*Worker{w})
	assert.GreaterOrEqual(t, len(attempts), 3)
	assert.Equal(t, 0, attempts[0])
	assert.Equal(t, 1, attempts[1])
	assert.Equal(t, 2, attempts[2])
}

func TestRunWorker_Single(t *testing.T) {
	var started atomic.Bool
	w := NewWorker("single", func(ctx WorkerContext) error {
		started.Store(true)
		<-ctx.Done()
		return ctx.Err()
	})

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	RunWorker(ctx, w)
	assert.True(t, started.Load())
}

func TestRun_WithJitter(t *testing.T) {
	var count atomic.Int32
	w := NewWorker("jittered", func(ctx WorkerContext) error {
		count.Add(1)
		return nil
	}).Every(10 * time.Millisecond).WithJitter(50)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := Run(ctx, []*Worker{w})
	assert.NoError(t, err)
	assert.GreaterOrEqual(t, int(count.Load()), 3, "jittered worker should tick multiple times")
}

func TestRun_WithDefaultJitter(t *testing.T) {
	var count atomic.Int32
	w := NewWorker("default-jitter", func(ctx WorkerContext) error {
		count.Add(1)
		return nil
	}).Every(10 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := Run(ctx, []*Worker{w}, WithDefaultJitter(50))
	assert.NoError(t, err)
	assert.GreaterOrEqual(t, int(count.Load()), 3, "default jitter worker should tick multiple times")
}

func TestRun_WorkerJitterOverridesDefault(t *testing.T) {
	// Worker-level jitter=0 should override run-level default and produce
	// exact intervals (no jitter).
	var count atomic.Int32
	w := NewWorker("no-jitter", func(ctx WorkerContext) error {
		count.Add(1)
		return nil
	}).Every(10 * time.Millisecond).WithJitter(0)

	ctx, cancel := context.WithTimeout(context.Background(), 55*time.Millisecond)
	defer cancel()

	err := Run(ctx, []*Worker{w}, WithDefaultJitter(50))
	assert.NoError(t, err)
	// With 0% jitter on 10ms interval in 55ms: should tick at least 3 times.
	assert.GreaterOrEqual(t, int(count.Load()), 3)
}

func TestRun_WithInitialDelay(t *testing.T) {
	start := time.Now()
	var firstTick atomic.Int64

	w := NewWorker("delayed", func(ctx WorkerContext) error {
		firstTick.CompareAndSwap(0, time.Since(start).Nanoseconds())
		return nil
	}).Every(10 * time.Millisecond).WithInitialDelay(50 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	err := Run(ctx, []*Worker{w})
	assert.NoError(t, err)

	delay := time.Duration(firstTick.Load())
	assert.GreaterOrEqual(t, delay, 40*time.Millisecond, "first tick should be delayed")
}
