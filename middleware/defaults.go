package middleware

import "github.com/go-coldbrew/workers"

// DefaultInterceptors returns the standard observability stack:
// [Recover], [LogContext], [Tracing], [Slog].
//
// Usage:
//
//	workers.Run(ctx, myWorkers,
//	    workers.WithInterceptors(middleware.DefaultInterceptors()...),
//	)
func DefaultInterceptors() []workers.Middleware {
	return []workers.Middleware{
		Recover(nil),
		LogContext(),
		Tracing(),
		Slog(),
	}
}
