package workers

import (
	"context"
	"errors"
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
