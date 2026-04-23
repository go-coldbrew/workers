package middleware

import (
	"context"

	"github.com/go-coldbrew/tracing"
	"github.com/go-coldbrew/workers"
)

// Tracing returns middleware that creates an OTEL span for each worker cycle
// using [tracing.NewInternalSpan]. The span is named "worker:<name>:cycle"
// and tagged with "worker.name".
//
// This is distinct from the per-worker-lifetime span created by the framework
// in workerRunService.Serve. For periodic workers, each tick gets its own
// trace span, making cycle-level latency visible in your tracing backend.
func Tracing() workers.Middleware {
	return func(next workers.CycleHandler) workers.CycleHandler {
		return workers.CycleFunc(func(ctx context.Context, info *workers.WorkerInfo) error {
			span, ctx := tracing.NewInternalSpan(ctx, "worker:"+info.Name+":cycle")
			defer span.Finish()
			span.SetTag("worker.name", info.Name)

			err := next.RunCycle(ctx, info)
			if err != nil {
				_ = span.SetError(err)
			}
			return err
		})
	}
}
