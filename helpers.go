package workers

import "time"

// EveryInterval wraps fn in a ticker loop that calls fn at the given interval.
// Returns when ctx is cancelled. If fn returns an error, EveryInterval returns
// that error (the supervisor decides whether to restart based on WithRestart).
func EveryInterval(d time.Duration, fn func(WorkerContext) error) func(WorkerContext) error {
	return func(ctx WorkerContext) error {
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
