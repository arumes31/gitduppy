package handlers

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gitduppy/gitduppy/internal/database"
	"github.com/gitduppy/gitduppy/pkg/response"
)

// HealthHandler handles health check requests
type HealthHandler struct {
	version   string
	buildTime string
	startTime time.Time
}

// NewHealthHandler creates a new health handler
func NewHealthHandler(version, buildTime string) *HealthHandler {
	return &HealthHandler{
		version:   version,
		buildTime: buildTime,
		startTime: time.Now(),
	}
}

// GetHealth handles GET /api/v1/health
func (h *HealthHandler) GetHealth(c *gin.Context) {
	db := database.GetDB()
	dbStatus := "disconnected"
	if db != nil {
		sqlDB, err := db.DB()
		if err == nil && sqlDB.Ping() == nil {
			dbStatus = "connected"
		}
	}

	uptime := time.Since(h.startTime)

	response.Success(c, gin.H{
		"status":         "healthy",
		"version":        h.version,
		"build_time":     h.buildTime,
		"uptime_seconds": int(uptime.Seconds()),
		"database":       dbStatus,
	})
}

// GetHealthLive handles GET /api/v1/health/live
func (h *HealthHandler) GetHealthLive(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status": "ok",
	})
}

// GetHealthReady handles GET /api/v1/health/ready
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
