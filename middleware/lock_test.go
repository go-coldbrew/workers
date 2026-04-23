package middleware

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-coldbrew/workers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockLocker implements Locker for testing.
type mockLocker struct {
	acquireResult bool
	acquireErr    error
	releaseErr    error
	acquireCalls  atomic.Int32
	releaseCalls  atomic.Int32
	lastKey       string
	lastTTL       time.Duration
	releaseCtx    context.Context // captures the context passed to Release
}

func (m *mockLocker) Acquire(_ context.Context, key string, ttl time.Duration) (bool, error) {
	m.acquireCalls.Add(1)
	m.lastKey = key
	m.lastTTL = ttl
	return m.acquireResult, m.acquireErr
}

func (m *mockLocker) Release(ctx context.Context, _ string) error {
	m.releaseCalls.Add(1)
	m.releaseCtx = ctx
	return m.releaseErr
}

var noop = workers.CycleFunc(func(ctx context.Context, info *workers.WorkerInfo) error {
	return nil
})

func TestDistributedLock_Acquired(t *testing.T) {
	locker := &mockLocker{acquireResult: true}
	mw := DistributedLock(locker)

	called := false
	inner := workers.CycleFunc(func(ctx context.Context, info *workers.WorkerInfo) error {
		called = true
		return nil
	})

	info := &workers.WorkerInfo{Name: "locked"}
	err := mw(inner).RunCycle(context.Background(), info)
	require.NoError(t, err)
	assert.True(t, called, "inner should be called when lock acquired")
	assert.Equal(t, int32(1), locker.acquireCalls.Load())
	assert.Equal(t, int32(1), locker.releaseCalls.Load())
	assert.Equal(t, "worker-lock:locked", locker.lastKey)
	assert.Equal(t, 30*time.Second, locker.lastTTL)
}

func TestDistributedLock_NotAcquired_SkipsCycle(t *testing.T) {
	locker := &mockLocker{acquireResult: false}
	mw := DistributedLock(locker)

	inner := workers.CycleFunc(func(ctx context.Context, info *workers.WorkerInfo) error {
		t.Fatal("inner should not be called when lock not acquired")
		return nil
	})

	info := &workers.WorkerInfo{Name: "skipped"}
	err := mw(inner).RunCycle(context.Background(), info)
	assert.NoError(t, err, "should return nil when lock not acquired (skip)")
	assert.Equal(t, int32(0), locker.releaseCalls.Load(), "should not call Release")
}

func TestDistributedLock_NotAcquired_CustomCallback(t *testing.T) {
	locker := &mockLocker{acquireResult: false}
	var callbackName string
	mw := DistributedLock(locker, WithOnNotAcquired(func(_ context.Context, name string) error {
		callbackName = name
		return errors.New("lock busy")
	}))

	inner := workers.CycleFunc(func(ctx context.Context, info *workers.WorkerInfo) error {
		t.Fatal("inner should not be called")
		return nil
	})

	info := &workers.WorkerInfo{Name: "busy"}
	err := mw(inner).RunCycle(context.Background(), info)
	assert.EqualError(t, err, "lock busy")
	assert.Equal(t, "busy", callbackName)
}

func TestDistributedLock_AcquireError(t *testing.T) {
	locker := &mockLocker{acquireErr: errors.New("redis down")}
	mw := DistributedLock(locker)

	err := mw(noop).RunCycle(context.Background(), &workers.WorkerInfo{Name: "err"})
	assert.EqualError(t, err, "redis down")
}

func TestDistributedLock_ReleaseCalledOnInnerError(t *testing.T) {
	locker := &mockLocker{acquireResult: true}
	mw := DistributedLock(locker)

	inner := workers.CycleFunc(func(ctx context.Context, info *workers.WorkerInfo) error {
		return errors.New("inner failed")
	})

	info := &workers.WorkerInfo{Name: "release-on-err"}
	err := mw(inner).RunCycle(context.Background(), info)
	assert.EqualError(t, err, "inner failed")
	assert.Equal(t, int32(1), locker.releaseCalls.Load(), "Release must be called even on error")
}

func TestDistributedLock_ReleaseUsesWithoutCancel(t *testing.T) {
	locker := &mockLocker{acquireResult: true}
	mw := DistributedLock(locker)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before entering middleware

	_ = mw(noop).RunCycle(ctx, &workers.WorkerInfo{Name: "cancel-safe"})

	// The release context should not be cancelled.
	require.NotNil(t, locker.releaseCtx)
	assert.NoError(t, locker.releaseCtx.Err(), "Release ctx should use WithoutCancel")
}

func TestDistributedLock_CustomKeyAndTTL(t *testing.T) {
	locker := &mockLocker{acquireResult: true}
	mw := DistributedLock(locker,
		WithKeyFunc(func(name string) string { return "custom:" + name }),
		WithTTLFunc(func(_ string) time.Duration { return 5 * time.Minute }),
	)

	_ = mw(noop).RunCycle(context.Background(), &workers.WorkerInfo{Name: "custom"})

	assert.Equal(t, "custom:custom", locker.lastKey)
	assert.Equal(t, 5*time.Minute, locker.lastTTL)
}
