package workers

import (
	"math/rand/v2"
	"time"
)

// EveryInterval wraps fn in a ticker loop that calls fn at the given interval.
// Returns when ctx is cancelled. If fn returns an error, EveryInterval returns
// that error (the supervisor decides whether to restart based on WithRestart).
func EveryInterval(d time.Duration, fn func(WorkerContext) error) func(WorkerContext) error {
	return everyIntervalWithDelay(d, 0, fn)
}

// everyIntervalWithDelay is the internal implementation of EveryInterval
// with optional initial delay support. When delay > 0, it sleeps before
// the first tick.
func everyIntervalWithDelay(d time.Duration, delay time.Duration, fn func(WorkerContext) error) func(WorkerContext) error {
	return func(ctx WorkerContext) error {
		if err := waitDelay(ctx, delay); err != nil {
			return err
		}

		ticker := time.NewTicker(d)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-ticker.C:
				if err := fn(ctx); err != nil {
					return err
				}
			}
		}
	}
}

// EveryIntervalWithJitter wraps fn in a loop that calls fn at variable intervals.
// Each interval is jittered: the actual duration is uniformly distributed in
// [base*(1-percent/100), base*(1+percent/100)), clamped to a minimum of 1ms.
// Uses time.Timer with Reset instead of Ticker for variable delays.
func EveryIntervalWithJitter(base time.Duration, jitterPercent int, fn func(WorkerContext) error) func(WorkerContext) error {
	return everyIntervalWithJitter(base, jitterPercent, 0, fn)
}

// everyIntervalWithJitter is the internal implementation with initial delay support.
func everyIntervalWithJitter(base time.Duration, jitterPercent int, delay time.Duration, fn func(WorkerContext) error) func(WorkerContext) error {
	return func(ctx WorkerContext) error {
		if err := waitDelay(ctx, delay); err != nil {
			return err
		}

		d := jitteredDuration(base, jitterPercent)
		timer := time.NewTimer(d)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-timer.C:
				if err := fn(ctx); err != nil {
					return err
				}
				timer.Reset(jitteredDuration(base, jitterPercent))
			}
		}
	}
}

// waitDelay sleeps for the given duration, returning early with ctx.Err()
// if the context is cancelled. Returns nil immediately if d <= 0.
func waitDelay(ctx WorkerContext, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	select {
	case <-ctx.Done():
		timer.Stop()
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// jitteredDuration applies ±percent jitter to a base duration.
// Result is clamped to a minimum of 1ms.
func jitteredDuration(base time.Duration, percent int) time.Duration {
	if percent <= 0 {
		return base
	}
	spread := int64(base) * int64(percent) / 100
	// Uniform random in [base-spread, base+spread)
	jittered := int64(base) - spread + rand.Int64N(2*spread+1)
	if jittered < int64(time.Millisecond) {
		jittered = int64(time.Millisecond)
	}
	return time.Duration(jittered)
}

// ChannelWorker consumes items from ch one at a time, calling fn for each.
// Returns when ctx is cancelled or ch is closed.
func ChannelWorker[T any](ch <-chan T, fn func(WorkerContext, T) error) func(WorkerContext) error {
	return func(ctx WorkerContext) error {
		for {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case item, ok := <-ch:
				if !ok {
					return nil // channel closed
				}
				if err := fn(ctx, item); err != nil {
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
func BatchChannelWorker[T any](ch <-chan T, maxSize int, maxDelay time.Duration, fn func(WorkerContext, []T) error) func(WorkerContext) error {
	return func(ctx WorkerContext) error {
		batch := make([]T, 0, maxSize)
		timer := time.NewTimer(maxDelay)
		timer.Stop() // don't start until first item

		flush := func() error {
			if len(batch) == 0 {
				return nil
			}
			err := fn(ctx, batch)
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
					timer.Stop()
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
