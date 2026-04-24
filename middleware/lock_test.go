package middleware

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/go-coldbrew/workers"
	"github.com/stretchr/testify/assert"
)

type mockLocker struct {
	mu         sync.Mutex
	acquired   bool
	acquireErr error
	acquireKey string
	acquireTTL time.Duration
	released   bool
	releaseKey string
}

func (m *mockLocker) Acquire(_ context.Context, key string, ttl time.Duration) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.acquireKey = key
	m.acquireTTL = ttl
	if m.acquireErr != nil {
		return false, m.acquireErr
	}
	return m.acquired, nil
}

func (m *mockLocker) Release(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.released = true
	m.releaseKey = key
	return nil
}

func TestDistributedLock_AcquiresAndReleases(t *testing.T) {
	locker := &mockLocker{acquired: true}
	mw := DistributedLock(locker)

	called := false
	info := workers.NewWorkerInfo("locked", 0)
	err := mw(context.Background(), info, func(_ context.Context, _ *workers.WorkerInfo) error {
		called = true
		return nil
	})

	assert.NoError(t, err)
	assert.True(t, called)

	locker.mu.Lock()
	defer locker.mu.Unlock()
	assert.True(t, locker.released)
	assert.Equal(t, "worker-lock:locked", locker.releaseKey)
}

func TestDistributedLock_SkipsWhenNotAcquired(t *testing.T) {
	locker := &mockLocker{acquired: false}
	mw := DistributedLock(locker)

	called := false
	info := workers.NewWorkerInfo("skipped", 0)
	err := mw(context.Background(), info, func(_ context.Context, _ *workers.WorkerInfo) error {
		called = true
		return nil
	})

	assert.NoError(t, err, "default onNotAcquired skips silently")
	assert.False(t, called, "next should not be called when lock not acquired")
}

func TestDistributedLock_OnNotAcquiredCallback(t *testing.T) {
	locker := &mockLocker{acquired: false}
	var gotName string
	mw := DistributedLock(locker, WithOnNotAcquired(func(_ context.Context, name string) error {
		gotName = name
		return errors.New("lock held")
	}))

	info := workers.NewWorkerInfo("contested", 0)
	err := mw(context.Background(), info, func(_ context.Context, _ *workers.WorkerInfo) error {
		return nil
	})

	assert.EqualError(t, err, "lock held")
	assert.Equal(t, "contested", gotName)
}

func TestDistributedLock_AcquireError(t *testing.T) {
	locker := &mockLocker{acquireErr: errors.New("redis down")}
	mw := DistributedLock(locker)

	info := workers.NewWorkerInfo("broken", 0)
	err := mw(context.Background(), info, func(_ context.Context, _ *workers.WorkerInfo) error {
		return nil
	})

	assert.EqualError(t, err, "redis down")
}

func TestDistributedLock_CustomKeyAndTTL(t *testing.T) {
	locker := &mockLocker{acquired: true}
	mw := DistributedLock(locker,
		WithKeyFunc(func(name string) string { return "custom:" + name }),
		WithTTLFunc(func(_ string) time.Duration { return time.Minute }),
	)

	info := workers.NewWorkerInfo("custom", 0)
	err := mw(context.Background(), info, func(_ context.Context, _ *workers.WorkerInfo) error {
		return nil
	})

	assert.NoError(t, err)

	locker.mu.Lock()
	defer locker.mu.Unlock()
	assert.Equal(t, "custom:custom", locker.acquireKey)
	assert.Equal(t, time.Minute, locker.acquireTTL)
	assert.Equal(t, "custom:custom", locker.releaseKey)
}
