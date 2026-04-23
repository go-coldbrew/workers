package workers

import (
	"context"
	"math/rand/v2"
	"time"
)

// EveryInterval wraps fn in a timer loop that calls fn at the given interval.
// Returns when ctx is cancelled. If fn returns an error, EveryInterval returns
// that error (the supervisor decides whether to restart based on [Worker.WithRestart]).
func EveryInterval(d time.Duration, fn CycleFunc) CycleFunc {
	return everyIntervalWithJitter(d, 0, 0, fn)
}

// everyIntervalWithJitter wraps fn in a timer loop with configurable jitter
// and initial delay.
//
// On each tick: spread = base * jitterPercent / 100; jittered = base - spread + rand(2*spread).
// The effective interval is clamped to a minimum of 1ms.
// Uses [time.NewTimer] + Reset for variable intervals (not time.Ticker).
//
// If jitterPercent is 0, the interval is fixed (no randomization).
// If initialDelay > 0, the first tick is delayed by that amount instead of
// the computed interval.
func everyIntervalWithJitter(base time.Duration, jitterPercent int, initialDelay time.Duration, fn CycleFunc) CycleFunc {
	base = max(base, time.Millisecond)
	return func(ctx context.Context, info *WorkerInfo) error {
		computeInterval := func() time.Duration {
			if jitterPercent <= 0 {
				return base
			}
			spread := int64(base) * int64(jitterPercent) / 100
			jittered := int64(base) - spread + rand.Int64N(2*spread+1)
			return time.Duration(max(jittered, int64(time.Millisecond)))
		}

		var firstInterval time.Duration
		if initialDelay > 0 {
			firstInterval = initialDelay
		} else {
			firstInterval = computeInterval()
		}

		timer := time.NewTimer(firstInterval)
		defer timer.Stop()

		for {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-timer.C:
				if err := fn(ctx, info); err != nil {
					return err
				}
				timer.Reset(computeInterval())
			}
		}
	}
}

// ChannelWorker consumes items from ch one at a time, calling fn for each.
// Returns when ctx is cancelled or ch is closed.
func ChannelWorker[T any](ch <-chan T, fn func(ctx context.Context, info *WorkerInfo, item T) error) CycleFunc {
	return func(ctx context.Context, info *WorkerInfo) error {
		for {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case item, ok := <-ch:
				if !ok {
					return nil // channel closed
				}
				if err := fn(ctx, info, item); err != nil {
					return err
				}
			}
		}
	}
}

// BatchChannelWorker collects items from ch into batches and calls fn when
// either the batch reaches maxSize or maxDelay elapses since the first item
// in the current batch — whichever comes first. Flushes any partial batch
// on context cancellation or channel close before returning.
func BatchChannelWorker[T any](ch <-chan T, maxSize int, maxDelay time.Duration, fn func(ctx context.Context, info *WorkerInfo, batch []T) error) CycleFunc {
	maxSize = max(maxSize, 1)
	return func(ctx context.Context, info *WorkerInfo) error {
		batch := make([]T, 0, maxSize)
		timer := time.NewTimer(maxDelay)
		timer.Stop() // don't start until first item

		flush := func() error {
			if len(batch) == 0 {
				return nil
			}
			err := fn(ctx, info, batch)
			batch = batch[:0]
			return err
		}

		for {
			select {
			case <-ctx.Done():
				// Flush remaining items before exit.
				_ = flush()
				return ctx.Err()

			case item, ok := <-ch:
				if !ok {
					// Channel closed — flush and return.
					return flush()
				}
				if len(batch) == 0 {
					timer.Reset(maxDelay)
				}
				batch = append(batch, item)
				if len(batch) >= maxSize {
					if !timer.Stop() {
						select {
						case <-timer.C:
						default:
						}
					}
					if err := flush(); err != nil {
						return err
					}
				}

			case <-timer.C:
				if err := flush(); err != nil {
					return err
				}
			}
		}
	}
}
