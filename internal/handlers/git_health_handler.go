package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/gitduppy/gitduppy/internal/services"
)

// GitHealthHandler handles git server health check requests.
type GitHealthHandler struct {
	healthService *services.HealthService
}

// NewGitHealthHandler creates a new git health handler.
func NewGitHealthHandler(healthService *services.HealthService) *GitHealthHandler {
	return &GitHealthHandler{
		healthService: healthService,
	}
}

// GetGitServerHealth handles GET /api/v1/health/git-servers.
func (h *GitHealthHandler) GetGitServerHealth(c *gin.Context) {
	healthChecks, err := h.healthService.GetLatestHealthChecks(c)
	if err != nil {
		respondServiceError(c, err)
		return
	}

	// If no health checks exist yet, return default status for known servers
	if len(healthChecks) == 0 {
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
			"message": "No health checks performed yet",
		})
		return
	}

	// Build response from health checks
	servers := gin.H{}
	for _, hc := range healthChecks {
		reachable := hc.Status == "healthy"
		responseTime := 0
		if hc.ResponseTimeMs != nil {
			responseTime = *hc.ResponseTimeMs
		}
		errorMsg := ""
		if hc.ErrorMessage != nil {
			errorMsg = *hc.ErrorMessage
		}
		servers[hc.TargetURL] = gin.H{
			"status":        hc.Status,
			"reachable":     reachable,
			"response_time": responseTime,
			"error_message": errorMsg,
			"checked_at":    hc.CheckedAt,
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"servers": servers,
	})
}
