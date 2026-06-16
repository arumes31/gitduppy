package gitops

import (
	"log"
	"time"

	"github.com/gitduppy/gitduppy/internal/database"
	"github.com/gitduppy/gitduppy/internal/models"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// CleanupWorker handles periodic cleanup of old logs and jobs
type CleanupWorker struct {
	db        *gorm.DB
	logger    *zap.Logger
	done      chan bool
	interval  time.Duration
	retention time.Duration
}

// CleanupConfig holds configuration for the cleanup worker
type CleanupConfig struct {
	Interval  time.Duration
	Retention time.Duration
}

// DefaultCleanupConfig returns default cleanup configuration
func DefaultCleanupConfig() *CleanupConfig {
	return &CleanupConfig{
		Interval:  24 * time.Hour,
		Retention: 30 * 24 * time.Hour, // 30 days
	}
}

// NewCleanupWorker creates a new cleanup worker
func NewCleanupWorker(config *CleanupConfig) *CleanupWorker {
	if config == nil {
		config = DefaultCleanupConfig()
	}
	return &CleanupWorker{
		db:        database.GetDB(),
		logger:    zap.L().Named("cleanup-worker"),
		done:      make(chan bool),
		interval:  config.Interval,
		retention: config.Retention,
	}
}

// Start starts the cleanup worker
func (w *CleanupWorker) Start() {
	w.logger.Info("starting cleanup worker", zap.Duration("interval", w.interval), zap.Duration("retention", w.retention))
	go w.run()
}

// Stop stops the cleanup worker
func (w *CleanupWorker) Stop() {
	w.logger.Info("stopping cleanup worker")
	w.done <- true
}

// run is the main cleanup loop
func (w *CleanupWorker) run() {
	// Run cleanup immediately on start
	w.performCleanup()

	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			w.performCleanup()
		case <-w.done:
			return
		}
	}
}

// performCleanup removes old clone logs, completed jobs, and expired sessions data
func (w *CleanupWorker) performCleanup() {
	w.logger.Info("performing cleanup of old data")

	cutoff := time.Now().Add(-w.retention)

	// Clean up old completed clone jobs (success, failed, cancelled)
	result := w.db.Where("status IN ? AND completed_at < ?", []string{"success", "failed", "cancelled"}, cutoff).Delete(&models.CloneJob{})
	if result.Error != nil {
		w.logger.Error("failed to cleanup clone jobs", zap.Error(result.Error))
	} else {
		log.Printf("Cleaned up %d old clone jobs", result.RowsAffected)
	}

	// Clean up old clone logs for deleted jobs
	logResult := w.db.Where("created_at < ?", cutoff).Delete(&models.CloneLog{})
	if logResult.Error != nil {
		w.logger.Error("failed to cleanup clone logs", zap.Error(logResult.Error))
	} else {
		log.Printf("Cleaned up %d old clone logs", logResult.RowsAffected)
	}

	// Clean up old webhook deliveries
	deliveryResult := w.db.Where("delivered_at < ?", cutoff).Delete(&models.WebhookDelivery{})
	if deliveryResult.Error != nil {
		w.logger.Error("failed to cleanup webhook deliveries", zap.Error(deliveryResult.Error))
	} else {
		log.Printf("Cleaned up %d old webhook deliveries", deliveryResult.RowsAffected)
	}

	// Clean up old audit logs
	auditResult := w.db.Where("created_at < ?", cutoff).Delete(&models.AuditLog{})
	if auditResult.Error != nil {
		w.logger.Error("failed to cleanup audit logs", zap.Error(auditResult.Error))
	} else {
		log.Printf("Cleaned up %d old audit logs", auditResult.RowsAffected)
	}

	// Clean up expired sessions
	sessionResult := w.db.Where("expires_at < ?", time.Now()).Delete(&models.Session{})
	if sessionResult.Error != nil {
		w.logger.Error("failed to cleanup sessions", zap.Error(sessionResult.Error))
	} else {
		log.Printf("Cleaned up %d expired sessions", sessionResult.RowsAffected)
	}

	w.logger.Info("cleanup completed")
}
