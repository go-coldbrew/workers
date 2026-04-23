package middleware

import (
	"context"
	"fmt"

	"github.com/go-coldbrew/workers"
)

// Recover catches panics per-cycle, calls onPanic (if non-nil), and returns
// an error. The panic does not propagate to the supervisor — even if
// onPanic itself panics.
func Recover(onPanic func(name string, v any)) workers.Middleware {
	return func(ctx context.Context, info *workers.WorkerInfo, next workers.CycleFunc) (retErr error) {
		defer func() {
			if v := recover(); v != nil {
				retErr = fmt.Errorf("panic in worker %s: %v", info.Name(), v)
				if onPanic != nil {
					func() {
						defer func() {
							if hookPanic := recover(); hookPanic != nil {
								retErr = fmt.Errorf("%w; onPanic callback panicked: %v", retErr, hookPanic)
							}
						}()
						onPanic(info.Name(), v)
					}()
				}
			}
		}()
		return next(ctx, info)
	}
}
