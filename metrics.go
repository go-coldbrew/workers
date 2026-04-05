package workers

import (
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	promMetricsMu    sync.RWMutex
	promMetricsCache = map[string]*prometheusMetrics{}
)

// Metrics collects worker lifecycle metrics.
// Implement this interface to provide custom metrics (e.g., Datadog, StatsD).
// Use BaseMetrics to disable metrics, or NewPrometheusMetrics for the built-in
// Prometheus implementation.
type Metrics interface {
	WorkerStarted(name string)
	WorkerStopped(name string)
	WorkerPanicked(name string)
	WorkerFailed(name string, err error)
	WorkerRestarted(name string, attempt int)
	ObserveRunDuration(name string, duration time.Duration)
	SetActiveWorkers(count int)
}

// BaseMetrics provides no-op implementations of all Metrics methods.
// Embed it in custom Metrics implementations so that new methods added
// to the Metrics interface in future versions get safe no-op defaults
// instead of breaking your build:
//
//	type myMetrics struct {
//	    workers.BaseMetrics // forward-compatible
//	    client *statsd.Client
//	}
//
//	func (m *myMetrics) WorkerStarted(name string) {
//	    m.client.Incr("worker.started", []string{"worker:" + name}, 1)
//	}
type BaseMetrics struct{}

func (BaseMetrics) WorkerStarted(string)                     {}
func (BaseMetrics) WorkerStopped(string)                     {}
func (BaseMetrics) WorkerPanicked(string)                    {}
func (BaseMetrics) WorkerFailed(string, error)               {}
func (BaseMetrics) WorkerRestarted(string, int)              {}
func (BaseMetrics) ObserveRunDuration(string, time.Duration) {}
func (BaseMetrics) SetActiveWorkers(int)                     {}

// prometheusMetrics implements Metrics using Prometheus counters, histograms,
// and gauges registered via promauto.
type prometheusMetrics struct {
	started     *prometheus.CounterVec
	stopped     *prometheus.CounterVec
	panicked    *prometheus.CounterVec
	failed      *prometheus.CounterVec
	restarted   *prometheus.CounterVec
	runDuration *prometheus.HistogramVec
	activeCount prometheus.Gauge
}

// NewPrometheusMetrics creates a Metrics implementation backed by Prometheus.
// The namespace is prepended to all metric names (e.g., "myapp" →
// "myapp_worker_started_total"). Metrics are auto-registered with the
// default Prometheus registry. Safe to call multiple times with the same
// namespace — returns the cached instance. The cache is process-global;
// use a small number of static namespaces (not per-request/tenant values).
func NewPrometheusMetrics(namespace string) Metrics {
	promMetricsMu.RLock()
	if m, ok := promMetricsCache[namespace]; ok {
		promMetricsMu.RUnlock()
		return m
	}
	promMetricsMu.RUnlock()

	promMetricsMu.Lock()
	defer promMetricsMu.Unlock()
	// Double-check after acquiring write lock.
	if m, ok := promMetricsCache[namespace]; ok {
		return m
	}
	m := &prometheusMetrics{
		started: promauto.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "worker_started_total",
			Help:      "Total number of worker starts.",
		}, []string{"worker"}),
		stopped: promauto.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "worker_stopped_total",
			Help:      "Total number of worker stops.",
		}, []string{"worker"}),
		panicked: promauto.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "worker_panicked_total",
			Help:      "Total number of worker panics.",
		}, []string{"worker"}),
		failed: promauto.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "worker_failed_total",
			Help:      "Total number of worker failures.",
		}, []string{"worker"}),
		restarted: promauto.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "worker_restarted_total",
			Help:      "Total number of worker restarts.",
		}, []string{"worker"}),
		runDuration: promauto.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: namespace,
			Name:      "worker_run_duration_seconds",
			Help:      "Duration of worker run cycles in seconds.",
			Buckets:   prometheus.DefBuckets,
		}, []string{"worker"}),
		activeCount: promauto.NewGauge(prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "worker_active_count",
			Help:      "Number of currently active workers.",
		}),
	}
	promMetricsCache[namespace] = m
	return m
}

func (p *prometheusMetrics) WorkerStarted(name string) {
	p.started.WithLabelValues(name).Inc()
}

func (p *prometheusMetrics) WorkerStopped(name string) {
	p.stopped.WithLabelValues(name).Inc()
}

func (p *prometheusMetrics) WorkerPanicked(name string) {
	p.panicked.WithLabelValues(name).Inc()
}

func (p *prometheusMetrics) WorkerFailed(name string, _ error) {
	p.failed.WithLabelValues(name).Inc()
}

func (p *prometheusMetrics) WorkerRestarted(name string, _ int) {
	p.restarted.WithLabelValues(name).Inc()
}

func (p *prometheusMetrics) ObserveRunDuration(name string, duration time.Duration) {
	p.runDuration.WithLabelValues(name).Observe(duration.Seconds())
}

func (p *prometheusMetrics) SetActiveWorkers(count int) {
	p.activeCount.Set(float64(count))
}
