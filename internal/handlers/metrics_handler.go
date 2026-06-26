package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// MetricsHandler handles Prometheus metrics requests.
type MetricsHandler struct {
	registry *prometheus.Registry
}

// NewMetricsHandler creates a new metrics handler with registered metrics.
func NewMetricsHandler() *MetricsHandler {
	registry := prometheus.NewRegistry()

	// Register standard Go metrics
	registry.MustRegister(collectors.NewGoCollector())
	registry.MustRegister(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))

	// Register custom application metrics
	registerCustomMetrics(registry)

	return &MetricsHandler{
		registry: registry,
	}
}

// GetRegistry returns the Prometheus registry.
func (h *MetricsHandler) GetRegistry() *prometheus.Registry {
	return h.registry
}

// MetricsHandlerFunc returns a gin handler that serves Prometheus metrics.
func (h *MetricsHandler) MetricsHandlerFunc() gin.HandlerFunc {
	handler := promhttp.HandlerFor(h.registry, promhttp.HandlerOpts{
		EnableOpenMetrics: true,
	})

	return func(c *gin.Context) {
		handler.ServeHTTP(c.Writer, c.Request)
	}
}

// GetMetrics handles GET /metrics.
func (h *MetricsHandler) GetMetrics(c *gin.Context) {
	handler := promhttp.HandlerFor(h.registry, promhttp.HandlerOpts{
		EnableOpenMetrics: true,
	})
	handler.ServeHTTP(c.Writer, c.Request)
}

// GetGitServerHealth handles GET /api/v1/health/git-servers.
func (h *MetricsHandler) GetGitServerHealth(c *gin.Context) {
	// This endpoint provides health status of configured git servers
	// In a full implementation, this would check connectivity to GitHub, GitLab, etc.
	c.JSON(http.StatusOK, gin.H{
		"servers": gin.H{
			"github": gin.H{
				"status":    "unknown",
				"reachable": false,
			},
			"gitlab": gin.H{
				"status":    "unknown",
				"reachable": false,
			},
			"bitbucket": gin.H{
				"status":    "unknown",
				"reachable": false,
			},
		},
	})
}

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
			Buckets: prometheus.DefBuckets,
		},
	)

	ActiveCloneJobs = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "gitduppy_active_clone_jobs",
			Help: "Number of currently active clone jobs",
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

func registerCustomMetrics(registry *prometheus.Registry) {
	registry.MustRegister(HTTPRequestsTotal)
	registry.MustRegister(HTTPRequestDuration)
	registry.MustRegister(CloneJobsTotal)
	registry.MustRegister(CloneJobDuration)
	registry.MustRegister(ActiveCloneJobs)
	registry.MustRegister(RepositoriesTotal)
	registry.MustRegister(WebhookDeliveriesTotal)
}
