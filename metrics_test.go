package workers

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// mockMetrics records all metric calls for assertions.
type mockMetrics struct {
	mu           sync.Mutex
	started      []string
	stopped      []string
	panicked     []string
	failed       []string
	restarted    []string
	durations    map[string]time.Duration
	activeCount  int
}

func newMockMetrics() *mockMetrics {
	return &mockMetrics{durations: make(map[string]time.Duration)}
}

func (m *mockMetrics) WorkerStarted(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.started = append(m.started, name)
}

func (m *mockMetrics) WorkerStopped(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stopped = append(m.stopped, name)
}

func (m *mockMetrics) WorkerPanicked(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.panicked = append(m.panicked, name)
}

func (m *mockMetrics) WorkerFailed(name string, _ error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failed = append(m.failed, name)
}

func (m *mockMetrics) WorkerRestarted(name string, _ int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.restarted = append(m.restarted, name)
}

func (m *mockMetrics) ObserveRunDuration(name string, d time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.durations[name] = d
}

func (m *mockMetrics) SetActiveWorkers(count int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.activeCount = count
}

func TestMetrics_StartedStopped(t *testing.T) {
	m := newMockMetrics()
	w := NewWorker("test-worker", func(ctx WorkerContext) error {
		<-ctx.Done()
		return ctx.Err()
	})

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	Run(ctx, []*Worker{w}, WithMetrics(m))

	m.mu.Lock()
	defer m.mu.Unlock()
	assert.Contains(t, m.started, "test-worker")
	assert.Contains(t, m.stopped, "test-worker")
	assert.NotEmpty(t, m.durations["test-worker"])
}

func TestMetrics_Failed(t *testing.T) {
	m := newMockMetrics()
	w := NewWorker("failer", func(ctx WorkerContext) error {
		return errors.New("boom")
	})

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	Run(ctx, []*Worker{w}, WithMetrics(m))

	m.mu.Lock()
	defer m.mu.Unlock()
	assert.Contains(t, m.failed, "failer")
}

func TestMetrics_Restarted(t *testing.T) {
	m := newMockMetrics()
	var attempts atomic.Int32
	w := NewWorker("restarter", func(ctx WorkerContext) error {
		if attempts.Add(1) <= 2 {
			return errors.New("fail")
		}
		<-ctx.Done()
		return ctx.Err()
	}).WithRestart(true)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	Run(ctx, []*Worker{w}, WithMetrics(m))

	m.mu.Lock()
	defer m.mu.Unlock()
	assert.NotEmpty(t, m.restarted, "should record restarts")
}

func TestMetrics_Inherited(t *testing.T) {
	m := newMockMetrics()
	manager := NewWorker("manager", func(ctx WorkerContext) error {
		ctx.Add(NewWorker("child", func(ctx WorkerContext) error {
			<-ctx.Done()
			return ctx.Err()
		}))
		<-ctx.Done()
		return ctx.Err()
	})

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	Run(ctx, []*Worker{manager}, WithMetrics(m))

	m.mu.Lock()
	defer m.mu.Unlock()
	assert.Contains(t, m.started, "manager")
	assert.Contains(t, m.started, "child", "child should inherit parent metrics")
}

func TestMetrics_ChildOverride(t *testing.T) {
	parentM := newMockMetrics()
	childM := newMockMetrics()

	manager := NewWorker("manager", func(ctx WorkerContext) error {
		ctx.Add(NewWorker("child", func(ctx WorkerContext) error {
			<-ctx.Done()
			return ctx.Err()
		}).WithMetrics(childM))
		<-ctx.Done()
		return ctx.Err()
	})

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	Run(ctx, []*Worker{manager}, WithMetrics(parentM))

	parentM.mu.Lock()
	assert.Contains(t, parentM.started, "manager")
	assert.NotContains(t, parentM.started, "child", "child should NOT use parent metrics")
	parentM.mu.Unlock()

	childM.mu.Lock()
	assert.Contains(t, childM.started, "child", "child should use its own metrics")
	childM.mu.Unlock()
}

func TestMetrics_NoopDefault(t *testing.T) {
	// Run without WithMetrics — should not panic.
	w := NewWorker("noop-test", func(ctx WorkerContext) error {
		<-ctx.Done()
		return ctx.Err()
	})

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	Run(ctx, []*Worker{w}) // no WithMetrics — uses NoopMetrics
}
