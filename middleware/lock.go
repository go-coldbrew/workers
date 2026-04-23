package middleware

import (
	"context"
	"time"

	"github.com/go-coldbrew/workers"
)

// Locker abstracts a distributed lock backend (Redis, etcd, Consul, etc.).
type Locker interface {
	// Acquire attempts to acquire a lock with the given key and TTL.
	// Returns true if acquired, false if the lock is held by another instance.
	Acquire(ctx context.Context, key string, ttl time.Duration) (bool, error)
	// Release releases the lock for the given key.
	Release(ctx context.Context, key string) error
}

// LockOption configures the DistributedLock middleware.
type LockOption func(*lockConfig)

type lockConfig struct {
	keyFunc       func(name string) string
	ttlFunc       func(name string) time.Duration
	onNotAcquired func(ctx context.Context, name string) error
}

// WithKeyFunc sets the function that generates the lock key from the worker name.
// Default: "worker-lock:" + name.
func WithKeyFunc(fn func(name string) string) LockOption {
	return func(c *lockConfig) { c.keyFunc = fn }
}

// WithTTLFunc sets the function that determines lock TTL from the worker name.
// Default: 30 seconds.
func WithTTLFunc(fn func(name string) time.Duration) LockOption {
	return func(c *lockConfig) { c.ttlFunc = fn }
}

// WithOnNotAcquired sets a callback invoked when the lock is held by another
// instance. The return value becomes the cycle's error.
// Default: return nil (skip the cycle silently).
func WithOnNotAcquired(fn func(ctx context.Context, name string) error) LockOption {
	return func(c *lockConfig) { c.onNotAcquired = fn }
}

// DistributedLock returns middleware that acquires a distributed lock before
// each worker cycle. If the lock cannot be acquired, the cycle is skipped
// (returns nil by default, or the result of OnNotAcquired).
// The lock is released after the cycle completes, using context.WithoutCancel
// so that context cancellation does not prevent release.
func DistributedLock(locker Locker, opts ...LockOption) workers.Middleware {
	cfg := &lockConfig{
		keyFunc: func(name string) string {
			return "worker-lock:" + name
		},
		ttlFunc: func(_ string) time.Duration {
			return 30 * time.Second
		},
	}
	for _, opt := range opts {
		opt(cfg)
	}

	return func(next workers.CycleHandler) workers.CycleHandler {
		return workers.CycleFunc(func(ctx context.Context, info *workers.WorkerInfo) error {
			key := cfg.keyFunc(info.Name)
			ttl := cfg.ttlFunc(info.Name)

			acquired, err := locker.Acquire(ctx, key, ttl)
			if err != nil {
				return err
			}
			if !acquired {
				if cfg.onNotAcquired != nil {
					return cfg.onNotAcquired(ctx, info.Name)
				}
				return nil // skip this cycle
			}
			defer func() {
				// Use WithoutCancel so cancellation doesn't prevent release.
				_ = locker.Release(context.WithoutCancel(ctx), key)
			}()
			return next.RunCycle(ctx, info)
		})
	}
}
