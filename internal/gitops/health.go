package gitops

import (
	"context"
	"log"
	"time"

	"github.com/gitduppy/gitduppy/internal/models"
	"github.com/gitduppy/gitduppy/internal/services"
)

// HealthMonitor handles periodic health checks.
type HealthMonitor struct {
	healthService *services.HealthService
	interval      time.Duration
}

// NewHealthMonitor creates a new health monitor.
func NewHealthMonitor(healthService *services.HealthService, interval time.Duration) *HealthMonitor {
	return &HealthMonitor{
		healthService: healthService,
		interval:      interval,
	}
}

// Start begins the health monitoring loop.
func (h *HealthMonitor) Start(ctx context.Context) {
	ticker := time.NewTicker(h.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			h.performHealthChecks(ctx)
		case <-ctx.Done():
			return
		}
	}
}

// performHealthChecks performs health checks on all configured git servers.
func (h *HealthMonitor) performHealthChecks(ctx context.Context) {
	// Get all unique repository URLs
	var urls []string
	db := h.healthService.DB()
	err := db.Model(&models.Repository{}).
		Distinct("url").
		Pluck("url", &urls).Error
	if err != nil {
		log.Printf("Failed to get repository URLs for health check: %v", err)
		return
	}

	// Check each URL
	for _, url := range urls {
		_, err := h.healthService.CheckGitServerHealth(ctx, url)
		if err != nil {
			log.Printf("Failed to check health for %s: %v", url, err)
		}
	}

	// Cleanup old health checks (keep last 30 days)
	cutoff := 30 * 24 * time.Hour
	if err := h.healthService.CleanupOldHealthChecks(ctx, cutoff); err != nil {
		log.Printf("Failed to cleanup old health checks: %v", err)
	}
}
