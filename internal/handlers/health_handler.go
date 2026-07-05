package handlers

import (
	"net/http"
	"os/exec"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gitduppy/gitduppy/internal/database"
	"github.com/gitduppy/gitduppy/internal/gitops"
	"github.com/gitduppy/gitduppy/pkg/response"
)

// HealthHandler handles health check requests.
type HealthHandler struct {
	version      string
	buildTime    string
	startTime    time.Time
	queueDepthFn func() int
}

// NewHealthHandler creates a new health handler.
func NewHealthHandler(version, buildTime string) *HealthHandler {
	return &HealthHandler{
		version:   version,
		buildTime: buildTime,
		startTime: time.Now(),
	}
}

// SetQueueDepthProvider wires a function that reports the current clone-job
// queue depth so it can be surfaced on the health endpoint.
func (h *HealthHandler) SetQueueDepthProvider(fn func() int) {
	h.queueDepthFn = fn
}

// GetHealth handles GET /api/v1/health.
func (h *HealthHandler) GetHealth(c *gin.Context) {
	db := database.GetDB()
	dbStatus := "disconnected"
	var poolStats gin.H
	if db != nil {
		sqlDB, err := db.DB()
		if err == nil && sqlDB.Ping() == nil {
			dbStatus = "connected"
			st := sqlDB.Stats()
			poolStats = gin.H{
				"open_connections": st.OpenConnections,
				"in_use":           st.InUse,
				"idle":             st.Idle,
				"wait_count":       st.WaitCount,
			}
		}
	}

	// git availability — the mirroring engine is useless without it.
	gitStatus := "unavailable"
	if _, err := exec.LookPath(gitops.GetGitExecutable()); err == nil {
		gitStatus = "available"
	}

	body := gin.H{
		"status":         "healthy",
		"version":        h.version,
		"build_time":     h.buildTime,
		"uptime_seconds": int(time.Since(h.startTime).Seconds()),
		"database":       dbStatus,
		"git":            gitStatus,
	}
	if poolStats != nil {
		body["db_pool"] = poolStats
	}
	if h.queueDepthFn != nil {
		body["clone_queue_depth"] = h.queueDepthFn()
	}
	response.Success(c, body)
}

// GetHealthLive handles GET /api/v1/health/live.
func (h *HealthHandler) GetHealthLive(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status": "ok",
	})
}

// GetHealthReady handles GET /api/v1/health/ready.
func (h *HealthHandler) GetHealthReady(c *gin.Context) {
	db := database.GetDB()
	if db == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"status": "not ready",
			"reason": "database not connected",
		})
		return
	}

	sqlDB, err := db.DB()
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"status": "not ready",
			"reason": "database connection error",
		})
		return
	}

	if err := sqlDB.Ping(); err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"status": "not ready",
			"reason": "database not reachable",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status": "ready",
	})
}
