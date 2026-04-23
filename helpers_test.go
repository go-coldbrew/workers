package workers

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestEveryInterval(t *testing.T) {
	var count atomic.Int32
	fn := EveryInterval(10*time.Millisecond, func(ctx WorkerContext) error {
		count.Add(1)
		return nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 55*time.Millisecond)
	defer cancel()

	wctx := newWorkerContext(ctx, "ticker", 0, nil, nil, nil)
	err := fn(wctx)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.GreaterOrEqual(t, int(count.Load()), 3)
}

func TestEveryInterval_ErrorStops(t *testing.T) {
	var count atomic.Int32
	fn := EveryInterval(10*time.Millisecond, func(ctx WorkerContext) error {
		if count.Add(1) >= 2 {
			return errors.New("done")
		}
		return nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	wctx := newWorkerContext(ctx, "ticker", 0, nil, nil, nil)
	err := fn(wctx)
	assert.EqualError(t, err, "done")
	assert.Equal(t, int32(2), count.Load())
}

func TestJitteredDuration_ZeroPercent(t *testing.T) {
	base := 100 * time.Millisecond
	for range 100 {
		d := jitteredDuration(base, 0)
		assert.Equal(t, base, d, "0%% jitter should return exact base")
	}
}

func TestJitteredDuration_Spread(t *testing.T) {
	base := 100 * time.Millisecond
	percent := 50
	lo := time.Duration(float64(base) * 0.5)
	hi := time.Duration(float64(base) * 1.5)

	for range 1000 {
		d := jitteredDuration(base, percent)
		assert.GreaterOrEqual(t, d, lo, "jittered duration below lower bound")
		assert.LessOrEqual(t, d, hi, "jittered duration above upper bound")
	}
}

func TestJitteredDuration_MinClamp(t *testing.T) {
	// Tiny base + large jitter should never produce <= 0.
	base := 2 * time.Millisecond
	percent := 100 // range: [0ms, 4ms) → clamped to [1ms, 4ms)

	for range 1000 {
		d := jitteredDuration(base, percent)
		assert.GreaterOrEqual(t, d, time.Millisecond, "jittered duration must be at least 1ms")
	}
}

func TestEveryIntervalWithJitter(t *testing.T) {
	var intervals []time.Duration
	var mu sync.Mutex
	last := time.Now()

	fn := EveryIntervalWithJitter(20*time.Millisecond, 50, func(ctx WorkerContext) error {
		now := time.Now()
		mu.Lock()
		intervals = append(intervals, now.Sub(last))
		last = now
		mu.Unlock()
		return nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	wctx := newWorkerContext(ctx, "jittered", 0, nil, nil, nil)
	_ = fn(wctx)

	mu.Lock()
	defer mu.Unlock()
	assert.GreaterOrEqual(t, len(intervals), 3, "should tick several times")

	// All intervals should be within [10ms, 30ms) given 50% jitter on 20ms base.
	// Allow generous tolerance for timer imprecision.
	for _, d := range intervals {
		assert.GreaterOrEqual(t, d, 5*time.Millisecond, "interval below lower bound")
		assert.Less(t, d, 50*time.Millisecond, "interval above upper bound")
	}
}

func TestEveryIntervalWithJitter_InitialDelay(t *testing.T) {
	start := time.Now()
	var firstTick time.Time

	fn := everyIntervalWithJitter(10*time.Millisecond, 0, 50*time.Millisecond, func(ctx WorkerContext) error {
		if firstTick.IsZero() {
			firstTick = time.Now()
		}
		return nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	wctx := newWorkerContext(ctx, "delayed", 0, nil, nil, nil)
	_ = fn(wctx)

	assert.False(t, firstTick.IsZero(), "should have ticked at least once")
	delay := firstTick.Sub(start)
	assert.GreaterOrEqual(t, delay, 40*time.Millisecond, "first tick should be delayed")
}

func TestEveryIntervalWithDelay_InitialDelay(t *testing.T) {
	start := time.Now()
	var firstTick time.Time

	fn := everyIntervalWithDelay(10*time.Millisecond, 50*time.Millisecond, func(ctx WorkerContext) error {
		if firstTick.IsZero() {
			firstTick = time.Now()
		}
		return nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	wctx := newWorkerContext(ctx, "delayed", 0, nil, nil, nil)
	_ = fn(wctx)

	assert.False(t, firstTick.IsZero(), "should have ticked at least once")
	delay := firstTick.Sub(start)
	assert.GreaterOrEqual(t, delay, 40*time.Millisecond, "first tick should be delayed")
}

func TestChannelWorker(t *testing.T) {
	ch := make(chan string, 3)
	ch <- "a"
	ch <- "b"
	ch <- "c"
	close(ch)

	var items []string
	fn := ChannelWorker(ch, func(ctx WorkerContext, item string) error {
		items = append(items, item)
		return nil
	})

	wctx := newWorkerContext(context.Background(), "ch", 0, nil, nil, nil)
	err := fn(wctx)
	assert.NoError(t, err)
	assert.Equal(t, []string{"a", "b", "c"}, items)
}

func TestChannelWorker_ContextCancel(t *testing.T) {
	ch := make(chan string) // unbuffered, blocks forever

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	fn := ChannelWorker(ch, func(ctx WorkerContext, item string) error {
		return nil
	})

	wctx := newWorkerContext(ctx, "ch", 0, nil, nil, nil)
	err := fn(wctx)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestBatchChannelWorker_MaxSize(t *testing.T) {
	ch := make(chan int, 10)
	for i := 0; i < 6; i++ {
		ch <- i
	}
	close(ch)

	var batches [][]int
	fn := BatchChannelWorker(ch, 3, time.Hour, func(ctx WorkerContext, batch []int) error {
		cp := make([]int, len(batch))
		copy(cp, batch)
		batches = append(batches, cp)
		return nil
	})

	wctx := newWorkerContext(context.Background(), "batch", 0, nil, nil, nil)
	err := fn(wctx)
	assert.NoError(t, err)
	assert.Equal(t, 2, len(batches))
	assert.Equal(t, []int{0, 1, 2}, batches[0])
	assert.Equal(t, []int{3, 4, 5}, batches[1])
}

func TestBatchChannelWorker_MaxDelay(t *testing.T) {
	ch := make(chan int, 10)
	ch <- 1
	ch <- 2

	var batches [][]int
	fn := BatchChannelWorker(ch, 100, 50*time.Millisecond, func(ctx WorkerContext, batch []int) error {
		cp := make([]int, len(batch))
		copy(cp, batch)
		batches = append(batches, cp)
		return nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	wctx := newWorkerContext(ctx, "batch", 0, nil, nil, nil)
	_ = fn(wctx)
	assert.GreaterOrEqual(t, len(batches), 1, "should flush on timer")
	assert.Equal(t, []int{1, 2}, batches[0])
}

func TestBatchChannelWorker_FlushOnClose(t *testing.T) {
	ch := make(chan int, 5)
	ch <- 1
	ch <- 2
	close(ch)

	var batches [][]int
	fn := BatchChannelWorker(ch, 100, time.Hour, func(ctx WorkerContext, batch []int) error {
		cp := make([]int, len(batch))
		copy(cp, batch)
		batches = append(batches, cp)
		return nil
	})

	wctx := newWorkerContext(context.Background(), "batch", 0, nil, nil, nil)
	err := fn(wctx)
	assert.NoError(t, err)
	assert.Equal(t, 1, len(batches))
	assert.Equal(t, []int{1, 2}, batches[0])
}
