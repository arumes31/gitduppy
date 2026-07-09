// Package metrics holds the application's Prometheus collectors in a neutral
// package so that any layer (HTTP middleware, the clone worker, services) can
// update them without creating an import cycle through the handlers package.
package metrics

import (
	"database/sql"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
)

// Custom Prometheus metrics.
//
//nolint:gochecknoglobals
var (
	HTTPRequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gitduppy_http_requests_total",
			Help: "Total number of HTTP requests",
		},
		[]string{"method", "path", "status"},
	)

	HTTPRequestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "gitduppy_http_request_duration_seconds",
			Help:    "HTTP request duration in seconds",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"method", "path"},
	)

	CloneJobsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gitduppy_clone_jobs_total",
			Help: "Total number of clone jobs",
		},
		[]string{"status", "trigger_type"},
	)

	CloneJobDuration = prometheus.NewHistogram(
		prometheus.HistogramOpts{
			Name:    "gitduppy_clone_job_duration_seconds",
			Help:    "Clone job duration in seconds",
			Buckets: []float64{1, 5, 10, 30, 60, 120, 300, 600, 1800, 3600},
		},
	)

	ActiveCloneJobs = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "gitduppy_active_clone_jobs",
			Help: "Number of currently active clone jobs",
		},
	)

	CloneQueueDepth = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "gitduppy_clone_queue_depth",
			Help: "Number of clone jobs waiting to be processed",
		},
	)

	RepositoriesTotal = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "gitduppy_repositories",
			Help: "Total number of repositories by status",
		},
		[]string{"status"},
	)

	StorageBytes = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "gitduppy_storage_bytes",
			Help: "Total on-disk size of mirrored repositories in bytes",
		},
	)

	WebhookDeliveriesTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gitduppy_webhook_deliveries_total",
			Help: "Total number of webhook deliveries",
		},
		[]string{"status"},
	)
)

// Register registers the Go/process collectors and all custom metrics on the
// given registry. Safe to call once per registry.
func Register(registry *prometheus.Registry) {
	registry.MustRegister(collectors.NewGoCollector())
	registry.MustRegister(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	registry.MustRegister(
		HTTPRequestsTotal,
		HTTPRequestDuration,
		CloneJobsTotal,
		CloneJobDuration,
		ActiveCloneJobs,
		CloneQueueDepth,
		RepositoriesTotal,
		StorageBytes,
		WebhookDeliveriesTotal,
	)
}

// NewDBStatsCollector returns a Prometheus collector that reports the
// database/sql connection-pool statistics on every scrape, so the gauges always
// reflect the pool's current state. provider returns the latest stats and false
// when the pool is unavailable (e.g. the DB is not connected yet).
func NewDBStatsCollector(provider func() (sql.DBStats, bool)) prometheus.Collector {
	return &dbStatsCollector{
		provider:     provider,
		open:         prometheus.NewDesc("gitduppy_db_open_connections", "Established database connections, both in use and idle", nil, nil),
		inUse:        prometheus.NewDesc("gitduppy_db_in_use_connections", "Database connections currently in use", nil, nil),
		idle:         prometheus.NewDesc("gitduppy_db_idle_connections", "Idle database connections", nil, nil),
		waitCount:    prometheus.NewDesc("gitduppy_db_wait_count_total", "Total number of connections waited for", nil, nil),
		waitDuration: prometheus.NewDesc("gitduppy_db_wait_duration_seconds_total", "Total time blocked waiting for a new connection, in seconds", nil, nil),
	}
}

type dbStatsCollector struct {
	provider     func() (sql.DBStats, bool)
	open         *prometheus.Desc
	inUse        *prometheus.Desc
	idle         *prometheus.Desc
	waitCount    *prometheus.Desc
	waitDuration *prometheus.Desc
}

func (c *dbStatsCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.open
	ch <- c.inUse
	ch <- c.idle
	ch <- c.waitCount
	ch <- c.waitDuration
}

func (c *dbStatsCollector) Collect(ch chan<- prometheus.Metric) {
	st, ok := c.provider()
	if !ok {
		return
	}
	ch <- prometheus.MustNewConstMetric(c.open, prometheus.GaugeValue, float64(st.OpenConnections))
	ch <- prometheus.MustNewConstMetric(c.inUse, prometheus.GaugeValue, float64(st.InUse))
	ch <- prometheus.MustNewConstMetric(c.idle, prometheus.GaugeValue, float64(st.Idle))
	// WaitCount/WaitDuration are cumulative since process start — expose as counters.
	ch <- prometheus.MustNewConstMetric(c.waitCount, prometheus.CounterValue, float64(st.WaitCount))
	ch <- prometheus.MustNewConstMetric(c.waitDuration, prometheus.CounterValue, st.WaitDuration.Seconds())
}
