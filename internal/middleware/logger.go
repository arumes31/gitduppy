package middleware

import (
	"fmt"
	"io"
	"os"
	"time"

	"github.com/gin-gonic/gin"
)

// LoggerConfig holds logger configuration.
type LoggerConfig struct {
	Output io.Writer
}

// DefaultLoggerConfig returns a default logger configuration.
func DefaultLoggerConfig() *LoggerConfig {
	return &LoggerConfig{
		Output: os.Stdout,
	}
}

// Logger returns a request logging middleware.
func Logger(config *LoggerConfig) gin.HandlerFunc {
	if config == nil {
		config = DefaultLoggerConfig()
	}

	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path
		query := c.Request.URL.RawQuery

		// Process request
		c.Next()

		// Calculate latency
		latency := time.Since(start)

		// Get status code
		statusCode := c.Writer.Status()

		// Get client IP
		clientIP := c.ClientIP()

		// Get method
		method := c.Request.Method

		// Format the log message
		logMsg := fmt.Sprintf("[GIN] %s | %3d | %13v | %15s | %-7s %s\n",
			time.Now().Format("2006/01/02 - 15:04:05"),
			statusCode,
			latency,
			clientIP,
			method,
			path,
		)

		if query != "" {
			logMsg = fmt.Sprintf("[GIN] %s | %3d | %13v | %15s | %-7s %s?%s\n",
				time.Now().Format("2006/01/02 - 15:04:05"),
				statusCode,
				latency,
				clientIP,
				method,
				path,
				query,
			)
		}

		fmt.Fprint(config.Output, logMsg)
	}
}

// GinLogger is a request logger based on gin's default logger, but it skips the
// health-check endpoints. Container/orchestrator probes hit these every few
// seconds, and logging each one buries the useful lines.
func GinLogger() gin.HandlerFunc {
	return gin.LoggerWithConfig(gin.LoggerConfig{
		SkipPaths: []string{
			"/api/v1/health",
			"/api/v1/health/live",
			"/api/v1/health/ready",
		},
	})
}
