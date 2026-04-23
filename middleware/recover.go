package middleware

import (
	"context"
	"fmt"

	"github.com/go-coldbrew/workers"
)

// Recover catches panics per-cycle, calls onPanic (if non-nil), and returns
// an error. The panic does not propagate to the supervisor.
func Recover(onPanic func(name string, v any)) workers.Middleware {
	return func(ctx context.Context, info *workers.WorkerInfo, next workers.CycleFunc) (retErr error) {
		defer func() {
			if v := recover(); v != nil {
				if onPanic != nil {
					onPanic(info.Name(), v)
				}
				retErr = fmt.Errorf("panic in worker %s: %v", info.Name(), v)
			}
		}()
		return next(ctx, info)
	}
}
