package gitops

import (
	"sync"
	"time"

	"github.com/gitduppy/gitduppy/internal/database"
	"github.com/gitduppy/gitduppy/internal/models"
	"github.com/google/uuid"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// reapOrphanedJobs marks clone jobs left behind by a previous process as failed,
// then recovers recently queued pending jobs. It runs once at worker startup.
//
// Orphans are jobs that no live worker could still own: a "running" job whose
// started_at is NULL or older than the clone timeout (a live worker would have
// timed out by then), and a "pending" job created before the same cutoff. These
// are marked "failed" with "orphaned by restart" rather than left to linger as
// permanent running/pending rows (which would also pin the scheduler's busy-set
// and block the repository from ever being re-queued). Requeue is not needed —
// the scheduler re-creates jobs for eligible repositories on its normal interval.
//
// The stale threshold (rather than reaping ALL running/pending at boot) preserves
// multi-replica safety: a co-running instance's freshly created or in-flight jobs
// are left untouched. Recent pending jobs (queued moments before this restart, or
// by a peer) are instead re-enqueued so they are not stranded.
func (w *CloneWorker) reapOrphanedJobs() {
	if w.db == nil {
		return
	}

	// Stale = older than the clone timeout plus a margin.
	timeout := time.Duration(w.config.CloneTimeout) * time.Second
	if timeout <= 0 {
		timeout = time.Duration(DefaultWorkerConfig().CloneTimeout) * time.Second
	}
	staleBefore := time.Now().Add(-timeout - time.Minute)

	reaped := w.db.Model(&models.CloneJob{}).
		Where("(status = ? AND (started_at IS NULL OR started_at < ?)) OR (status = ? AND created_at < ?)",
			"running", staleBefore, "pending", staleBefore).
		Updates(map[string]any{
			"status":       "failed",
			"completed_at": time.Now().UTC(),
			"output_log":   "orphaned by restart",
		})
	if reaped.Error != nil {
		w.logger.Error("failed to reap orphaned clone jobs", zap.Error(reaped.Error))
	} else if reaped.RowsAffected > 0 {
		w.logger.Warn("reaped orphaned clone jobs from a previous process",
			zap.Int64("count", reaped.RowsAffected))
	}

	// Recover pending jobs that are recent enough not to have been reaped so a
	// restart does not strand them (nothing else will dispatch an existing pending
	// row, and the scheduler skips repos that already have a pending job).
	var jobs []models.CloneJob
	if err := w.db.Where("status = ?", "pending").Order("created_at asc").Find(&jobs).Error; err != nil {
		w.logger.Error("failed to load pending jobs for requeue", zap.Error(err))
		return
	}
	if len(jobs) == 0 {
		return
	}
	w.logger.Info("requeuing pending clone jobs", zap.Int("count", len(jobs)))
	for i := range jobs {
		w.Enqueue(&jobs[i])
	}
}

// Scheduler handles scheduled clone jobs.
type Scheduler struct {
	db       *gorm.DB
	worker   *CloneWorker
	ticker   *time.Ticker
	done     chan struct{}
	stopOnce sync.Once
	logger   *zap.Logger
}

// schedulerTickInterval is how often the scheduler evaluates repositories. It is
// intentionally finer than the smallest allowed per-repo clone interval (5 min)
// so a repo configured for 5-minute syncs is not delayed by up to a full extra
// tick before its job is queued.
const schedulerTickInterval = time.Minute

// NewScheduler creates a new scheduler.
func NewScheduler(worker *CloneWorker) *Scheduler {
	return &Scheduler{
		db:     database.GetDB(),
		worker: worker,
		ticker: time.NewTicker(schedulerTickInterval),
		done:   make(chan struct{}),
		logger: zap.L().Named("scheduler"),
	}
}

// Start starts the scheduler.
func (s *Scheduler) Start() {
	s.logger.Info("starting scheduler")
	go s.run()
}

// Stop stops the scheduler. Closing done (guarded by Once) is non-blocking and
// safe even if Start was never called or Stop is called more than once — unlike
// the previous unbuffered send, which could deadlock in both cases.
func (s *Scheduler) Stop() {
	s.logger.Info("stopping scheduler")
	s.ticker.Stop()
	s.stopOnce.Do(func() { close(s.done) })
}

// run is the main scheduler loop.
func (s *Scheduler) run() {
	for {
		select {
		case <-s.ticker.C:
			s.scheduleCloneJobs()
		case <-s.done:
			return
		}
	}
}

// scheduleCloneJobs schedules clone jobs for all active repositories.
func (s *Scheduler) scheduleCloneJobs() {
	s.logger.Debug("checking for repositories to clone")

	// Skip scheduling while maintenance mode is enabled.
	var setting models.SystemSetting
	if err := s.db.Where("key = ?", "maintenance_mode").First(&setting).Error; err == nil && setting.Value == "true" {
		s.logger.Info("maintenance mode enabled, skipping scheduled clone jobs")
		return
	}

	var repos []models.Repository
	if err := s.db.Where("is_active = ? AND status != 'cloning'", true).Find(&repos).Error; err != nil {
		s.logger.Error("failed to fetch repositories", zap.Error(err))
		return
	}

	// Load the set of repositories that already have an unfinished job in a single
	// query, rather than issuing one COUNT per repo each tick. Used below to avoid
	// piling up duplicate clones into the same directory (a slow clone can span
	// several ticks). If this query fails the busy set would be empty and every
	// eligible repo would be re-enqueued despite already having a running/pending
	// job, so abort the tick rather than schedule duplicates.
	var busyIDs []uuid.UUID
	if err := s.db.Model(&models.CloneJob{}).
		Where("status IN ?", []string{"pending", "running"}).
		Distinct().
		Pluck("repository_id", &busyIDs).Error; err != nil {
		s.logger.Error("failed to load busy repositories, skipping tick to avoid duplicate clones", zap.Error(err))
		return
	}
	busy := make(map[uuid.UUID]struct{}, len(busyIDs))
	for _, id := range busyIDs {
		busy[id] = struct{}{}
	}

	now := time.Now()
	for _, repo := range repos {
		// Respect the retry backoff window: a repository whose last clone failed
		// carries a next_retry_at set with exponential backoff + jitter. Skipping
		// it until then stops a hard-failing repo (whose LastCloneAt stays nil, so
		// the interval check below is always satisfied) from being re-queued on
		// every tick and hammering the origin.
		if repo.NextRetryAt != nil && now.Before(*repo.NextRetryAt) {
			continue
		}

		// Check if clone interval has elapsed
		if repo.LastCloneAt == nil ||
			now.Sub(*repo.LastCloneAt) >= time.Duration(repo.CloneIntervalMinutes)*time.Minute {

			if _, inflight := busy[repo.ID]; inflight {
				continue
			}

			job := &models.CloneJob{
				ID:           uuid.New(),
				RepositoryID: repo.ID,
				TriggerType:  "scheduled",
				Status:       "pending",
				CreatedAt:    time.Now().UTC(),
			}
			// Only enqueue a job that actually persisted: the worker updates the
			// row by primary key, so an unpersisted job would silently no-op and
			// the repo would never clone. Skip this repo on a failed insert and
			// let the next tick retry it.
			if err := s.db.Create(job).Error; err != nil {
				s.logger.Error("failed to persist scheduled clone job", zap.String("repo", repo.Name), zap.Error(err))
				continue
			}
			s.worker.Enqueue(job)
			s.logger.Debug("enqueued clone job", zap.String("repo", repo.Name))
		}
	}
}
