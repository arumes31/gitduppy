package gitops

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/gitduppy/gitduppy/internal/database"
	"github.com/gitduppy/gitduppy/internal/models"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// CleanupWorker handles periodic cleanup of old logs and jobs.
type CleanupWorker struct {
	db        *gorm.DB
	logger    *zap.Logger
	done      chan bool
	interval  time.Duration
	retention time.Duration
}

// CleanupConfig holds configuration for the cleanup worker.
type CleanupConfig struct {
	Interval  time.Duration
	Retention time.Duration
}

// DefaultCleanupConfig returns default cleanup configuration.
func DefaultCleanupConfig() *CleanupConfig {
	return &CleanupConfig{
		Interval:  24 * time.Hour,
		Retention: 30 * 24 * time.Hour, // 30 days
	}
}

// NewCleanupWorker creates a new cleanup worker.
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

// Start starts the cleanup worker.
func (w *CleanupWorker) Start() {
	w.logger.Info("starting cleanup worker", zap.Duration("interval", w.interval), zap.Duration("retention", w.retention))
	go w.run()
}

// Stop stops the cleanup worker.
func (w *CleanupWorker) Stop() {
	w.logger.Info("stopping cleanup worker")
	w.done <- true
}

// run is the main cleanup loop.
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

// performCleanup removes old clone logs, completed jobs, and expired sessions data.
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
	sessionResult := w.db.Where("expiry < ?", time.Now()).Delete(&models.Session{})
	if sessionResult.Error != nil {
		w.logger.Error("failed to cleanup sessions", zap.Error(sessionResult.Error))
	} else {
		log.Printf("Cleaned up %d expired sessions", sessionResult.RowsAffected)
	}

	// Clean up old soft-deleted repositories (older than 30 days)
	var expiredRepos []models.Repository
	if err := w.db.Unscoped().Where("deleted_at IS NOT NULL AND deleted_at < ?", cutoff).Find(&expiredRepos).Error; err == nil {
		for _, repo := range expiredRepos {
			w.logger.Info("permanently deleting expired repository from paperbin", zap.String("repo", repo.Name), zap.String("id", repo.ID.String()))
			
			// 1. Delete related DeletedBranches
			_ = w.db.Where("repository_id = ?", repo.ID).Delete(&models.DeletedBranch{})
			// 2. Delete related CloneJobs
			_ = w.db.Where("repository_id = ?", repo.ID).Delete(&models.CloneJob{})
			// 3. Hard delete from DB
			if err := w.db.Unscoped().Delete(&repo).Error; err == nil {
				// 4. Purge folders on disk
				paperbinPath := filepath.Join(filepath.Dir(repo.StoragePath), "paperbin", repo.ID.String())
				_ = os.RemoveAll(paperbinPath)
				_ = os.RemoveAll(repo.StoragePath)
			}
		}
	}

	// Clean up old deleted branches (older than 30 days)
	var expiredBranches []models.DeletedBranch
	if err := w.db.Where("deleted_at < ?", cutoff).Find(&expiredBranches).Error; err == nil {
		for _, br := range expiredBranches {
			w.logger.Info("permanently deleting expired pruned branch from paperbin", zap.String("branch", br.BranchName), zap.String("repo_id", br.RepositoryID.String()))
			
			// Find repository path to delete git ref
			var repo models.Repository
			if err := w.db.Unscoped().First(&repo, br.RepositoryID).Error; err == nil {
				paperbinRef := "refs/paperbin/heads/" + br.BranchName
				_, _ = RunGitCommand(context.Background(), repo.StoragePath, "update-ref", "-d", paperbinRef)
			}
			
			// Delete DB record
			_ = w.db.Delete(&br)
		}
	}

	w.logger.Info("cleanup completed")
}
