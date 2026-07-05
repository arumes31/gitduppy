// Package metrics holds the application's Prometheus collectors in a neutral
// package so that any layer (HTTP middleware, the clone worker, services) can
// update them without creating an import cycle through the handlers package.
package metrics

import (
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
		WebhookDeliveriesTotal,
	)
}
