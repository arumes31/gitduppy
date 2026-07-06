package gitops

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"sync"
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
	done      chan struct{}
	stopOnce  sync.Once
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
		done:      make(chan struct{}),
		interval:  config.Interval,
		retention: config.Retention,
	}
}

// Start starts the cleanup worker.
func (w *CleanupWorker) Start() {
	w.logger.Info("starting cleanup worker", zap.Duration("interval", w.interval), zap.Duration("retention", w.retention))
	go w.run()
}

// Stop stops the cleanup worker. Closing done (guarded by Once) never blocks and
// is safe even if Start was never called or Stop is called twice — unlike the
// previous unbuffered send, which could block for the duration of an in-progress
// cleanup pass or deadlock forever if the loop was never started.
func (w *CleanupWorker) Stop() {
	w.logger.Info("stopping cleanup worker")
	w.stopOnce.Do(func() { close(w.done) })
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

	// Clean up old soft-deleted repositories based on custom retention policies
	var allDeletedRepos []models.Repository
	if err := w.db.Unscoped().Where("deleted_at IS NOT NULL").Find(&allDeletedRepos).Error; err == nil {
		for _, repo := range allDeletedRepos {
			retentionDays := repo.RetentionDays
			if retentionDays <= 0 {
				retentionDays = 30
			}
			repoCutoff := repo.DeletedAt.Time.Add(time.Duration(retentionDays) * 24 * time.Hour)
			if time.Now().After(repoCutoff) {
				w.logger.Info("permanently deleting expired repository from paperbin", zap.String("repo", repo.Name), zap.String("id", repo.ID.String()), zap.Int("retention_days", retentionDays))

				// Delete all DB records in a single transaction so the
				// DeletedBranch/CloneJob removals and the repo hard-delete
				// either all succeed or all roll back together.
				dbErr := w.db.Transaction(func(tx *gorm.DB) error {
					if err := tx.Where("repository_id = ?", repo.ID).Delete(&models.DeletedBranch{}).Error; err != nil {
						return err
					}
					if err := tx.Where("repository_id = ?", repo.ID).Delete(&models.CloneJob{}).Error; err != nil {
						return err
					}
					return tx.Unscoped().Delete(&repo).Error
				})
				if dbErr != nil {
					w.logger.Error("failed to purge expired repository from DB", zap.String("id", repo.ID.String()), zap.Error(dbErr))
					continue
				}

				// Only purge disk after the DB work has committed. Surface
				// any storage cleanup failures explicitly.
				paperbinPath := filepath.Join(filepath.Dir(repo.StoragePath), "paperbin", repo.ID.String())
				if err := os.Remove(paperbinPath + ".tar.gz"); err != nil && !os.IsNotExist(err) {
					w.logger.Error("failed to remove paperbin archive", zap.String("path", paperbinPath+".tar.gz"), zap.Error(err))
				}
				if err := os.RemoveAll(paperbinPath); err != nil {
					w.logger.Error("failed to remove paperbin directory", zap.String("path", paperbinPath), zap.Error(err))
				}
				if err := os.RemoveAll(repo.StoragePath); err != nil {
					w.logger.Error("failed to remove repository storage", zap.String("path", repo.StoragePath), zap.Error(err))
				}
			}
		}
	}

	// Clean up old deleted branches based on repository-specific retention policies
	var deletedBranches []models.DeletedBranch
	if err := w.db.Find(&deletedBranches).Error; err == nil {
		for _, br := range deletedBranches {
			var repo models.Repository
			retentionDays := 30
			if err := w.db.Unscoped().First(&repo, br.RepositoryID).Error; err == nil {
				if repo.RetentionDays > 0 {
					retentionDays = repo.RetentionDays
				}
			}

			branchCutoff := br.DeletedAt.Add(time.Duration(retentionDays) * 24 * time.Hour)
			if time.Now().After(branchCutoff) {
				w.logger.Info("permanently deleting expired pruned branch from paperbin", zap.String("branch", br.BranchName), zap.String("repo_id", br.RepositoryID.String()), zap.Int("retention_days", retentionDays))

				// Find repository path to delete git ref. Only remove the DB
				// row once the git ref has actually been deleted, otherwise the
				// commit would be unreachable while the record claims it is gone.
				var targetRepo models.Repository
				if err := w.db.Unscoped().First(&targetRepo, br.RepositoryID).Error; err != nil {
					w.logger.Error("failed to load repository for branch purge", zap.String("repo_id", br.RepositoryID.String()), zap.Error(err))
					continue
				}

				gitCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				paperbinRef := "refs/paperbin/heads/" + br.BranchName
				out, gitErr := RunGitCommand(gitCtx, targetRepo.StoragePath, "update-ref", "-d", paperbinRef)
				cancel()
				if gitErr != nil {
					w.logger.Error("failed to delete paperbin git ref, keeping DB record", zap.String("ref", paperbinRef), zap.String("output", out), zap.Error(gitErr))
					continue
				}

				// Delete DB record only after the git ref was removed.
				if err := w.db.Delete(&br).Error; err != nil {
					w.logger.Error("failed to delete pruned branch record", zap.String("branch", br.BranchName), zap.Error(err))
				}
			}
		}
	}

	w.logger.Info("cleanup completed")
}
