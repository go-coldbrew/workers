package workers

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestEveryInterval(t *testing.T) {
	var count atomic.Int32
	fn := EveryInterval(10*time.Millisecond, func(_ context.Context, _ *WorkerInfo) error {
		count.Add(1)
		return nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 55*time.Millisecond)
	defer cancel()

	info := &WorkerInfo{name: "ticker", attempt: 0}
	err := fn(ctx, info)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.GreaterOrEqual(t, int(count.Load()), 3)
}

func TestEveryInterval_ErrorStops(t *testing.T) {
	var count atomic.Int32
	fn := EveryInterval(10*time.Millisecond, func(_ context.Context, _ *WorkerInfo) error {
		if count.Add(1) >= 2 {
			return errors.New("done")
		}
		return nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	info := &WorkerInfo{name: "ticker", attempt: 0}
	err := fn(ctx, info)
	assert.EqualError(t, err, "done")
	assert.Equal(t, int32(2), count.Load())
}

func TestEveryInterval_Jitter(t *testing.T) {
	var count atomic.Int32
	fn := everyIntervalWithJitter(10*time.Millisecond, 50, 0, func(_ context.Context, _ *WorkerInfo) error {
		count.Add(1)
		return nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	info := &WorkerInfo{name: "jittery", attempt: 0}
	err := fn(ctx, info)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.GreaterOrEqual(t, int(count.Load()), 2, "should tick multiple times with jitter")
}

func TestEveryInterval_Jitter_InitialDelay(t *testing.T) {
	var firstTickTime time.Time
	start := time.Now()
	fn := everyIntervalWithJitter(10*time.Millisecond, 0, 50*time.Millisecond, func(_ context.Context, _ *WorkerInfo) error {
		if firstTickTime.IsZero() {
			firstTickTime = time.Now()
		}
		return nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	info := &WorkerInfo{name: "delayed", attempt: 0}
	_ = fn(ctx, info)

	delay := firstTickTime.Sub(start)
	assert.GreaterOrEqual(t, delay, 40*time.Millisecond, "first tick should be delayed")
}

func TestEveryInterval_Jitter_Clamped(t *testing.T) {
	// With 100% jitter on a 1ms base, the minimum should still be 1ms (clamped).
	var count atomic.Int32
	fn := everyIntervalWithJitter(time.Millisecond, 100, 0, func(_ context.Context, _ *WorkerInfo) error {
		count.Add(1)
		return nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	info := &WorkerInfo{name: "clamped", attempt: 0}
	_ = fn(ctx, info)
	assert.GreaterOrEqual(t, int(count.Load()), 1, "should tick at least once")
}

func TestEveryInterval_Jitter_VariableIntervals(t *testing.T) {
	// Run many ticks and verify intervals aren't all identical when jitter is enabled.
	var timestamps []time.Time
	fn := everyIntervalWithJitter(5*time.Millisecond, 50, 0, func(_ context.Context, _ *WorkerInfo) error {
		timestamps = append(timestamps, time.Now())
		return nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	info := &WorkerInfo{name: "variable", attempt: 0}
	_ = fn(ctx, info)

	if len(timestamps) < 5 {
		t.Skip("not enough ticks to verify variance")
	}

	// Compute intervals between consecutive timestamps.
	var intervals []time.Duration
	for i := 1; i < len(timestamps); i++ {
		intervals = append(intervals, timestamps[i].Sub(timestamps[i-1]))
	}

	// Compute standard deviation — should be > 0 with jitter.
	var sum float64
	for _, d := range intervals {
		sum += float64(d)
	}
	mean := sum / float64(len(intervals))
	var variance float64
	for _, d := range intervals {
		diff := float64(d) - mean
		variance += diff * diff
	}
	stddev := math.Sqrt(variance / float64(len(intervals)))
	assert.Greater(t, stddev, 0.0, "intervals should vary with jitter enabled")
}

func TestEveryIntervalWithJitter_ErrSkipTick(t *testing.T) {
	var count atomic.Int32
	fn := everyIntervalWithJitter(10*time.Millisecond, 0, 0, func(_ context.Context, _ *WorkerInfo) error {
		n := count.Add(1)
		if n == 1 {
			return ErrSkipTick // skip first tick
		}
		return nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()

	info := &WorkerInfo{name: "skiptick", attempt: 0}
	err := fn(ctx, info)
	assert.ErrorIs(t, err, context.DeadlineExceeded, "should not exit from ErrSkipTick")
	assert.GreaterOrEqual(t, int(count.Load()), 2, "should continue ticking after ErrSkipTick")
}

func TestEveryIntervalWithJitter_ErrSkipTick_Wrapped(t *testing.T) {
	var count atomic.Int32
	fn := everyIntervalWithJitter(10*time.Millisecond, 0, 0, func(_ context.Context, _ *WorkerInfo) error {
		n := count.Add(1)
		if n == 1 {
			return fmt.Errorf("db timeout: %w", ErrSkipTick)
		}
		return nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()

	info := &WorkerInfo{name: "skiptick-wrapped", attempt: 0}
	err := fn(ctx, info)
	assert.ErrorIs(t, err, context.DeadlineExceeded, "wrapped ErrSkipTick should also be caught")
	assert.GreaterOrEqual(t, int(count.Load()), 2)
}

func TestChannelWorker(t *testing.T) {
	ch := make(chan string, 3)
	ch <- "a"
	ch <- "b"
	ch <- "c"
	close(ch)

	var items []string
	fn := ChannelWorker(ch, func(_ context.Context, _ *WorkerInfo, item string) error {
		items = append(items, item)
		return nil
	})

	info := &WorkerInfo{name: "ch", attempt: 0}
	err := fn(context.Background(), info)
	assert.ErrorIs(t, err, ErrDoNotRestart)
	assert.Equal(t, []string{"a", "b", "c"}, items)
}

func TestChannelWorker_ContextCancel(t *testing.T) {
	ch := make(chan string) // unbuffered, blocks forever

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	fn := ChannelWorker(ch, func(_ context.Context, _ *WorkerInfo, _ string) error {
		return nil
	})

	info := &WorkerInfo{name: "ch", attempt: 0}
	err := fn(ctx, info)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestBatchChannelWorker_MaxSize(t *testing.T) {
	ch := make(chan int, 10)
	for i := range 6 {
		ch <- i
	}
	close(ch)

	var batches [][]int
	fn := BatchChannelWorker(ch, 3, time.Hour, func(_ context.Context, _ *WorkerInfo, batch []int) error {
		cp := make([]int, len(batch))
		copy(cp, batch)
		batches = append(batches, cp)
		return nil
	})

	info := &WorkerInfo{name: "batch", attempt: 0}
	err := fn(context.Background(), info)
	assert.ErrorIs(t, err, ErrDoNotRestart)
	assert.Equal(t, 2, len(batches))
	assert.Equal(t, []int{0, 1, 2}, batches[0])
	assert.Equal(t, []int{3, 4, 5}, batches[1])
}

func TestBatchChannelWorker_MaxDelay(t *testing.T) {
	ch := make(chan int, 10)
	ch <- 1
	ch <- 2

	var batches [][]int
	fn := BatchChannelWorker(ch, 100, 50*time.Millisecond, func(_ context.Context, _ *WorkerInfo, batch []int) error {
		cp := make([]int, len(batch))
		copy(cp, batch)
		batches = append(batches, cp)
		return nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	info := &WorkerInfo{name: "batch", attempt: 0}
	_ = fn(ctx, info)
	assert.GreaterOrEqual(t, len(batches), 1, "should flush on timer")
	assert.Equal(t, []int{1, 2}, batches[0])
}

func TestBatchChannelWorker_FlushOnClose(t *testing.T) {
	ch := make(chan int, 5)
	ch <- 1
	ch <- 2
	close(ch)

	var batches [][]int
	fn := BatchChannelWorker(ch, 100, time.Hour, func(_ context.Context, _ *WorkerInfo, batch []int) error {
		cp := make([]int, len(batch))
		copy(cp, batch)
		batches = append(batches, cp)
		return nil
	})

	info := &WorkerInfo{name: "batch", attempt: 0}
	err := fn(context.Background(), info)
	assert.ErrorIs(t, err, ErrDoNotRestart)
	assert.Equal(t, 1, len(batches))
	assert.Equal(t, []int{1, 2}, batches[0])
}
