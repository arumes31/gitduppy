package gitops

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gitduppy/gitduppy/internal/database"
	"github.com/gitduppy/gitduppy/internal/models"
	"github.com/gitduppy/gitduppy/pkg/validator"
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
	// auditRetention applies to audit_logs only; 0 keeps them forever.
	auditRetention time.Duration
	basePath       string
}

// CleanupConfig holds configuration for the cleanup worker.
type CleanupConfig struct {
	Interval  time.Duration
	Retention time.Duration
	// AuditRetention is how long audit log entries (security records) are kept,
	// independent of Retention (logging.audit_retention_days). 0 keeps them
	// forever.
	AuditRetention time.Duration
	// BasePath is the storage root under which the shared object "pools"
	// directory lives (see GitOperations.BasePath / GetPoolPath). It must be
	// passed explicitly rather than derived from a repository's StoragePath,
	// since row order from the DB is not deterministic and StoragePath can be
	// overridden to a non-canonical location.
	BasePath string
}

// DefaultCleanupConfig returns default cleanup configuration.
func DefaultCleanupConfig() *CleanupConfig {
	return &CleanupConfig{
		Interval:       24 * time.Hour,
		Retention:      30 * 24 * time.Hour, // 30 days
		AuditRetention: 90 * 24 * time.Hour, // 90 days
	}
}

// NewCleanupWorker creates a new cleanup worker.
func NewCleanupWorker(config *CleanupConfig) *CleanupWorker {
	if config == nil {
		config = DefaultCleanupConfig()
	}
	return &CleanupWorker{
		db:             database.GetDB(),
		logger:         zap.L().Named("cleanup-worker"),
		done:           make(chan struct{}),
		interval:       config.Interval,
		retention:      config.Retention,
		auditRetention: config.AuditRetention,
		basePath:       config.BasePath,
	}
}

// Start starts the cleanup worker.
func (w *CleanupWorker) Start() {
	w.logger.Info("starting cleanup worker", zap.Duration("interval", w.interval), zap.Duration("retention", w.retention),
		zap.Duration("audit_retention", w.auditRetention))
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

// purge logs the outcome of a pre-scoped Delete: the error (errLabel) or the
// affected-row count (successLabel), both through the configured zap pipeline.
// Callers pass the *gorm.DB returned by w.db.Where(...).Delete(&model{}).
func (w *CleanupWorker) purge(errLabel, successLabel string, result *gorm.DB) {
	if result.Error != nil {
		w.logger.Error("failed to cleanup "+errLabel, zap.Error(result.Error))
	} else {
		w.logger.Info("cleaned up "+successLabel, zap.Int64("count", result.RowsAffected))
	}
}

// auditLogCutoff returns the creation time before which audit logs are deleted,
// and false when retention is not positive (keep audit logs forever).
func auditLogCutoff(now time.Time, retention time.Duration) (time.Time, bool) {
	if retention <= 0 {
		return time.Time{}, false
	}
	return now.Add(-retention), true
}

// performCleanup removes old clone logs, completed jobs, and expired sessions data.
func (w *CleanupWorker) performCleanup() {
	w.logger.Info("performing cleanup of old data")

	cutoff := time.Now().Add(-w.retention)

	// Clean up old completed clone jobs (success, failed, cancelled)
	w.purge("clone jobs", "old clone jobs",
		w.db.Where("status IN ? AND completed_at < ?", []string{"success", "failed", "cancelled"}, cutoff).Delete(&models.CloneJob{}))

	// Clean up old clone logs for deleted jobs
	w.purge("clone logs", "old clone logs",
		w.db.Where("created_at < ?", cutoff).Delete(&models.CloneLog{}))

	// Clean up old webhook deliveries
	w.purge("webhook deliveries", "old webhook deliveries",
		w.db.Where("delivered_at < ?", cutoff).Delete(&models.WebhookDelivery{}))

	// Clean up old audit logs on their own (longer) retention; they are
	// security records, so 0 disables deletion entirely.
	if auditCutoff, ok := auditLogCutoff(time.Now(), w.auditRetention); ok {
		w.purge("audit logs", "old audit logs",
			w.db.Where("created_at < ?", auditCutoff).Delete(&models.AuditLog{}))
	}

	// Clean up expired sessions
	w.purge("sessions", "expired sessions",
		w.db.Where("expiry < ?", time.Now()).Delete(&models.Session{}))

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

				// Only purge disk after the DB work has committed, and only for
				// paths inside the sharded storage tree. Surface any storage
				// cleanup failures explicitly.
				if !validator.IsRepoStoragePath(w.basePath, repo.StoragePath) {
					w.logger.Error("skipping disk cleanup: repository storage path is outside the storage root",
						zap.String("id", repo.ID.String()), zap.String("path", repo.StoragePath))
					continue
				}
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

				if !validator.IsRepoStoragePath(w.basePath, targetRepo.StoragePath) {
					w.logger.Error("skipping pruned branch purge: repository storage path is outside the storage root",
						zap.String("repo_id", br.RepositoryID.String()), zap.String("path", targetRepo.StoragePath))
					continue
				}

				gitCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				gitErr := ReleaseArchivalRef(gitCtx, w.db, targetRepo.StoragePath, &br)
				cancel()
				if gitErr != nil {
					w.logger.Error("failed to delete paperbin git ref, keeping DB record", zap.String("ref", br.ArchivalRef()), zap.Error(gitErr))
					continue
				}

				// Delete DB record only after the git ref was removed.
				if err := w.db.Delete(&br).Error; err != nil {
					w.logger.Error("failed to delete pruned branch record", zap.String("branch", br.BranchName), zap.Error(err))
				}
			}
		}
	}

	w.cleanupPools()

	w.logger.Info("cleanup completed")
}

// ReleaseArchivalRef deletes the git ref that protects a pruned branch's commit
// for one deletion event, ahead of that event's DeletedBranch row being removed
// (on expiry, restore or permanent delete).
//
// The ref is deleted only while it still points at the event's own commit, and
// atomically so (`git update-ref -d <ref> <expected-sha>`), so it can never
// remove a ref that now protects a different event. Legacy rows (written before
// per-event refs, ArchiveRef == "") share one branch-name-derived ref; that ref
// is left alone while any other legacy row of the same branch still exists, and
// is otherwise deleted only if it still holds this row's commit. A ref that is
// already gone, or that points elsewhere, is not an error: this event's commit is
// simply no longer protected by it.
func ReleaseArchivalRef(ctx context.Context, db *gorm.DB, repoPath string, br *models.DeletedBranch) error {
	ref := br.ArchivalRef()
	if !strings.HasPrefix(ref, "refs/") {
		return fmt.Errorf("refusing to delete non-ref archival name %q", ref)
	}
	if !validator.ValidateCommitSHA(br.CommitSHA) {
		return fmt.Errorf("deleted branch record %s has an invalid commit sha", br.ID)
	}

	if br.HasLegacyArchivalRef() {
		var others int64
		if err := db.WithContext(ctx).Model(&models.DeletedBranch{}).
			Where("repository_id = ? AND branch_name = ? AND id <> ? AND (archive_ref = '' OR archive_ref IS NULL)",
				br.RepositoryID, br.BranchName, br.ID).
			Count(&others).Error; err != nil {
			return fmt.Errorf("check shared legacy archival ref: %w", err)
		}
		if others > 0 {
			return nil
		}
	}

	// Nothing on disk means nothing left to protect or delete.
	if _, statErr := os.Stat(repoPath); errors.Is(statErr, os.ErrNotExist) {
		return nil
	}

	// ref always starts with "refs/", so it cannot be parsed as an option.
	current, err := RunGitCommand(ctx, repoPath, "rev-parse", "--verify", "--quiet", ref)
	if err != nil {
		// rev-parse --verify --quiet exits non-zero without output when the ref
		// does not exist; anything else is a real failure.
		if strings.TrimSpace(current) == "" {
			return nil
		}
		return fmt.Errorf("resolve archival ref: %w", err)
	}
	if strings.TrimSpace(current) != br.CommitSHA {
		return nil
	}
	if out, err := RunGitCommand(ctx, repoPath, "update-ref", "-d", ref, br.CommitSHA); err != nil {
		return fmt.Errorf("delete archival ref: %w: %s", err, strings.TrimSpace(out))
	}
	return nil
}

// cleanupPools performs auto-gc on shared object pools to keep packfiles compact.
func (w *CleanupWorker) cleanupPools() {
	if w.basePath == "" {
		return
	}
	poolsDir := filepath.Join(w.basePath, "pools")
	if _, err := os.Stat(poolsDir); err != nil {
		if !os.IsNotExist(err) {
			w.logger.Error("failed to stat pools directory", zap.String("path", poolsDir), zap.Error(err))
		}
		return
	}
	walkErr := filepath.Walk(poolsDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			w.logger.Error("failed to walk pools directory entry", zap.String("path", path), zap.Error(err))
			return nil
		}
		if !info.IsDir() {
			return nil
		}
		if _, statErr := os.Stat(filepath.Join(path, "HEAD")); statErr == nil {
			gitCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			out, gcErr := RunGitCommand(gitCtx, path, "gc", "--auto")
			cancel()
			if gcErr != nil {
				w.logger.Error("failed to run git gc --auto on pool", zap.String("path", path), zap.String("output", out), zap.Error(gcErr))
			}
			return filepath.SkipDir
		}
		return nil
	})
	if walkErr != nil {
		w.logger.Error("failed to walk pools directory", zap.String("path", poolsDir), zap.Error(walkErr))
	}
}
