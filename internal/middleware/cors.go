package middleware

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

// CORSConfig holds CORS configuration.
type CORSConfig struct {
	AllowOrigins     []string
	AllowMethods     []string
	AllowHeaders     []string
	AllowCredentials bool
	MaxAge           int
}

// DefaultCORSConfig returns a default CORS configuration.
func DefaultCORSConfig() *CORSConfig {
	return &CORSConfig{
		AllowOrigins:     []string{"*"},
		AllowMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Accept", "Authorization", "X-Requested-With"},
		AllowCredentials: true,
		MaxAge:           86400,
	}
}

// CORS returns a CORS middleware function.
func CORS(config *CORSConfig) gin.HandlerFunc {
	if config == nil {
		config = DefaultCORSConfig()
	}

	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")

		// Check if origin is allowed. Track whether it matched an explicit
		// allow-list entry (vs. a "*" wildcard): credentials may only be
		// combined with a specific, echoed Origin — never with a wildcard.
		allowed := false
		explicit := false
		for _, o := range config.AllowOrigins {
			if o == origin {
				allowed = true
				explicit = true
				break
			}
			if o == "*" {
				allowed = true
			}
		}

		if !allowed {
			c.Next()
			return
		}

		// Set CORS headers. Reflecting the caller's Origin together with
		// Access-Control-Allow-Credentials: true would expose authenticated
		// responses to any site. So credentials are only granted for an
		// explicit origin match; a wildcard match emits "*" without credentials
		// (as the CORS spec forbids "*" + credentials).
		if explicit && origin != "" {
			c.Header("Access-Control-Allow-Origin", origin)
			if config.AllowCredentials {
				c.Header("Access-Control-Allow-Credentials", "true")
			}
		} else {
			c.Header("Access-Control-Allow-Origin", "*")
		}

		c.Header("Access-Control-Allow-Methods", strings.Join(config.AllowMethods, ", "))
		c.Header("Access-Control-Allow-Headers", strings.Join(config.AllowHeaders, ", "))
		c.Header("Access-Control-Max-Age", strconv.Itoa(config.MaxAge))

		// Handle preflight requests
		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}

		c.Next()
	}
}
