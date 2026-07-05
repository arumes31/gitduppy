package middleware

import (
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gitduppy/gitduppy/internal/metrics"
)

// Metrics records request counts and latency into the Prometheus collectors.
// It labels by the matched route template (c.FullPath()) rather than the raw
// URL so high-cardinality path parameters (repo IDs, SHAs) do not explode the
// metric series count.
func Metrics() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()

		path := c.FullPath()
		if path == "" {
			path = "unmatched" // 404s and unrouted paths collapse into one series
		}
		metrics.HTTPRequestDuration.WithLabelValues(c.Request.Method, path).Observe(time.Since(start).Seconds())
		metrics.HTTPRequestsTotal.WithLabelValues(c.Request.Method, path, strconv.Itoa(c.Writer.Status())).Inc()
	}
}
