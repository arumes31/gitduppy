package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/gitduppy/gitduppy/internal/metrics"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// MetricsHandler handles Prometheus metrics requests.
type MetricsHandler struct {
	registry *prometheus.Registry
}

// NewMetricsHandler creates a new metrics handler with registered metrics.
func NewMetricsHandler() *MetricsHandler {
	registry := prometheus.NewRegistry()
	metrics.Register(registry)
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

