package middleware

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/go-coldbrew/workers"
)

// Locker abstracts a distributed lock backend (e.g., Redis, etcd, Consul).
type Locker interface {
	// Acquire attempts to acquire a lock for the given key with a TTL.
	// Returns true if the lock was acquired, false if held by another instance.
	Acquire(ctx context.Context, key string, ttl time.Duration) (bool, error)
	// Release releases a previously acquired lock.
	Release(ctx context.Context, key string) error
}

// LockOption configures [DistributedLock] behavior.
type LockOption func(*lockConfig)

type lockConfig struct {
	keyFunc       func(name string) string
	ttlFunc       func(name string) time.Duration
	onNotAcquired func(ctx context.Context, name string) error
}

// WithKeyFunc sets a custom function to derive the lock key from the worker name.
// Default: "worker-lock:<name>".
func WithKeyFunc(fn func(name string) string) LockOption {
	return func(c *lockConfig) { c.keyFunc = fn }
}

// WithTTLFunc sets a custom function to derive the lock TTL from the worker name.
// Default: 30s.
func WithTTLFunc(fn func(name string) time.Duration) LockOption {
	return func(c *lockConfig) { c.ttlFunc = fn }
}

// WithOnNotAcquired sets a callback invoked when the lock is held by another
// instance. The cycle is skipped. Default: skip silently (return nil).
func WithOnNotAcquired(fn func(ctx context.Context, name string) error) LockOption {
	return func(c *lockConfig) { c.onNotAcquired = fn }
}

// DistributedLock acquires a distributed lock before each cycle. If the lock
// is held by another instance, the cycle is skipped (or the onNotAcquired
// callback is invoked). Release uses [context.WithoutCancel] so that context
// cancellation does not prevent lock cleanup.
func DistributedLock(locker Locker, opts ...LockOption) workers.Middleware {
	cfg := &lockConfig{
		keyFunc:       func(name string) string { return "worker-lock:" + name },
		ttlFunc:       func(_ string) time.Duration { return 30 * time.Second },
		onNotAcquired: func(_ context.Context, _ string) error { return nil },
	}
	for _, o := range opts {
		o(cfg)
	}

	return func(ctx context.Context, info *workers.WorkerInfo, next workers.CycleFunc) (retErr error) {
		key := cfg.keyFunc(info.GetName())
		ttl := cfg.ttlFunc(info.GetName())

		acquired, err := locker.Acquire(ctx, key, ttl)
		if err != nil {
			return err
		}
		if !acquired {
			return cfg.onNotAcquired(ctx, info.GetName())
		}
		defer func() {
			releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), ttl)
			defer cancel()
			if releaseErr := locker.Release(releaseCtx, key); releaseErr != nil {
				retErr = errors.Join(retErr, fmt.Errorf("release lock %q: %w", key, releaseErr))
			}
		}()
		return next(ctx, info)
	}
}
