package gitops

import (
	"context"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gitduppy/gitduppy/internal/database"
	"github.com/gitduppy/gitduppy/internal/metrics"
	"github.com/gitduppy/gitduppy/internal/models"
	"github.com/gitduppy/gitduppy/pkg/crypto"
	"github.com/google/uuid"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// LogHub manages subscribers for real-time repository progress logs.
type LogHub struct {
	mu          sync.Mutex
	subscribers map[string][]chan string
}

// GlobalLogHub is the global instance for logging subscription.
var GlobalLogHub = &LogHub{
	subscribers: make(map[string][]chan string),
}

// Subscribe returns a channel of logs for a repository.
func (h *LogHub) Subscribe(repoID string) chan string {
	h.mu.Lock()
	defer h.mu.Unlock()
	ch := make(chan string, 100)
	h.subscribers[repoID] = append(h.subscribers[repoID], ch)
	return ch
}

// Unsubscribe removes a channel subscription. The channel is closed and dropped
// from the subscriber slice; once a repository has no subscribers left its map
// entry is deleted so the map does not accumulate empty slices for every
// repository that was ever streamed.
func (h *LogHub) Unsubscribe(repoID string, ch chan string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	subs := h.subscribers[repoID]
	for i, sub := range subs {
		if sub == ch {
			// Assign the append result back to subs (same backing slice) so gocritic's
			// appendAssign is satisfied; this removes element i in place.
			subs = append(subs[:i], subs[i+1:]...)
			if len(subs) == 0 {
				delete(h.subscribers, repoID)
			} else {
				h.subscribers[repoID] = subs
			}
			close(ch)
			break
		}
	}
}

// Broadcast sends a message to all subscribers for a repository.
func (h *LogHub) Broadcast(repoID string, message string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, ch := range h.subscribers[repoID] {
		select {
		case ch <- message:
		default:
		}
	}
}

// WorkerConfig holds configuration for the clone worker.
type WorkerConfig struct {
	MaxConcurrent    int
	CloneTimeout     int
	RetryMaxAttempts int
	RetryBaseDelay   time.Duration
	DedupeEnabled    bool
}

// DefaultWorkerConfig returns default worker configuration.
func DefaultWorkerConfig() *WorkerConfig {
	return &WorkerConfig{
		MaxConcurrent:    3,
		CloneTimeout:     3600,
		RetryMaxAttempts: 3,
		RetryBaseDelay:   30 * time.Second,
		DedupeEnabled:    true,
	}
}

// CloneWorker handles background clone operations.
type CloneWorker struct {
	config        *WorkerConfig
	gitOps        *GitOperations
	db            *gorm.DB
	encryption    *crypto.EncryptionService
	webhookSender WebhookSender
	emailSender   EmailSender
	jobQueue      chan *models.CloneJob
	wg            sync.WaitGroup
	ctx           context.Context
	cancel        context.CancelFunc
	logger        *zap.Logger

	// Graceful shutdown: softStop is closed first to stop accepting new jobs while
	// in-flight jobs (tracked by inflight) are given shutdownGrace to finish before
	// their contexts are hard-cancelled via cancel(). softStopOnce guards the close.
	softStop      chan struct{}
	softStopOnce  sync.Once
	inflight      sync.WaitGroup
	shutdownGrace time.Duration

	// inflightMu serializes inflight.Add against Stop() setting stopping. A worker
	// admits a job (inflight.Add) only under this lock and only while stopping is
	// false; Stop() takes the same lock to set stopping before it calls
	// inflight.Wait(). That ordering makes every Add happen-before the Wait, closing
	// the sync.WaitGroup hazard of growing the counter from zero during a Wait.
	inflightMu sync.Mutex
	stopping   bool

	// repoLocks serializes clone/fetch work per on-disk repository path so two
	// jobs for the same repository never run git operations concurrently (which
	// would corrupt .git/index.lock, packed-refs, or the object store).
	repoLocks *keyedMutex

	// Overflow backlog drained by a single dispatcher goroutine so that a burst
	// larger than jobQueue's buffer never spawns a goroutine per job.
	pendingMu sync.Mutex
	pending   []*models.CloneJob
	notify    chan struct{}
}

// WebhookSender interface for sending webhook events.
type WebhookSender interface {
	SendEvent(ctx context.Context, eventType string, payload map[string]any) error
}

// EmailSender interface for sending email notifications.
type EmailSender interface {
	SendCloneFailureNotification(ctx context.Context, repo *models.Repository, job *models.CloneJob, err error) error
}

// defaultShutdownGrace bounds how long Stop() waits for in-flight clone jobs to
// finish before hard-cancelling their contexts. Cancelled jobs are put back to
// pending and recovered on the next start, so this is kept short to keep total
// shutdown time reasonable.
const defaultShutdownGrace = 20 * time.Second

// NewCloneWorker creates a new clone worker.
func NewCloneWorker(config *WorkerConfig, gitOps *GitOperations, encryption *crypto.EncryptionService) *CloneWorker {
	// context.Background is the correct root here: the worker pool's lifetime spans
	// the whole process, not any single request. cancel() is called on Stop().
	ctx, cancel := context.WithCancel(context.Background())
	return &CloneWorker{
		config:        config,
		gitOps:        gitOps,
		db:            database.GetDB(),
		encryption:    encryption,
		jobQueue:      make(chan *models.CloneJob, 128),
		notify:        make(chan struct{}, 1),
		ctx:           ctx,
		cancel:        cancel,
		logger:        zap.L().Named("clone-worker"),
		repoLocks:     newKeyedMutex(),
		softStop:      make(chan struct{}),
		shutdownGrace: defaultShutdownGrace,
	}
}

// enqueueAfter re-enqueues a job after the given delay. The waiting goroutine is
// tracked by the WaitGroup and cancelled on shutdown, so a deferred retry never
// fires after Stop() (which would touch the DB or dispatcher post-shutdown); the
// job is instead recovered from its persisted "pending" row on the next start.
func (w *CloneWorker) enqueueAfter(job *models.CloneJob, delay time.Duration) {
	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-timer.C:
			w.Enqueue(job)
		case <-w.ctx.Done():
		}
	}()
}

// SetNotificationServices sets the webhook and email notification services.
func (w *CloneWorker) SetNotificationServices(webhookSender WebhookSender, emailSender EmailSender) {
	w.webhookSender = webhookSender
	w.emailSender = emailSender
}

// Start starts the worker pool.
func (w *CloneWorker) Start() {
	w.logger.Info("starting clone worker pool", zap.Int("workers", w.config.MaxConcurrent))

	// Start worker goroutines
	for i := 0; i < w.config.MaxConcurrent; i++ {
		w.wg.Add(1)
		go w.worker(i)
	}

	// Single dispatcher drains the overflow backlog into the worker queue.
	w.wg.Add(1)
	go w.dispatch()

	// Reap clone jobs orphaned by a previous process (stale "running"/"pending"
	// rows) and recover recently queued pending jobs so a restart neither leaves
	// zombie rows nor strands work. See reapOrphanedJobs.
	w.reapOrphanedJobs()
}

// Stop stops the worker pool with a bounded graceful drain. First it stops
// accepting new work (softStop); then it gives in-flight jobs up to shutdownGrace
// to finish; then it hard-cancels the worker context (aborting any still-running
// git operation — those jobs are put back to "pending" for the next start) and
// waits for every goroutine to exit.
//
// The job channel is intentionally NOT closed: with the non-dropping Enqueue,
// closing it could race a blocked send and panic.
func (w *CloneWorker) Stop() {
	w.logger.Info("stopping clone worker pool")

	// Phase 1: stop accepting new jobs. In-flight jobs continue running.
	w.softStopOnce.Do(func() { close(w.softStop) })

	// Phase 2: mark the worker stopping under inflightMu so no further job can be
	// admitted, then give in-flight jobs a bounded grace period to complete. Setting
	// stopping here (not merely closing softStop above) is what guarantees
	// inflight.Add can never race this Wait: admit() takes the same lock and refuses
	// once stopping is set, so every prior Add happened-before this point.
	w.inflightMu.Lock()
	w.stopping = true
	w.inflightMu.Unlock()

	drained := make(chan struct{})
	go func() {
		w.inflight.Wait()
		close(drained)
	}()
	select {
	case <-drained:
		w.logger.Info("in-flight clone jobs finished within grace period")
	case <-time.After(w.shutdownGrace):
		w.logger.Warn("clone shutdown grace elapsed; cancelling in-flight jobs",
			zap.Duration("grace", w.shutdownGrace))
	}

	// Phase 3: cancel everything (idle workers, retry timers, any job still
	// running past the grace period) and wait for all goroutines to exit.
	w.cancel()
	w.wg.Wait()
}

// Enqueue adds a job to the queue. It never blocks and never drops a job: the job
// is appended to an in-memory backlog that a single dispatcher goroutine drains
// into the worker queue. This bounds overflow handling to one goroutine no matter
// how large a burst is (a goroutine-per-job fallback could otherwise spawn
// thousands under a bulk sync). Silently dropping jobs previously left
// repositories stuck in "pending".
func (w *CloneWorker) Enqueue(job *models.CloneJob) {
	w.pendingMu.Lock()
	w.pending = append(w.pending, job)
	w.pendingMu.Unlock()

	// Wake the dispatcher; the buffered notify channel coalesces bursts so we
	// never block here.
	select {
	case w.notify <- struct{}{}:
	default:
	}
	metrics.CloneQueueDepth.Set(float64(w.QueueDepth()))
	w.logger.Debug("job enqueued", zap.String("job_id", job.ID.String()))
}

// QueueDepth returns how many clone jobs are waiting to be processed: the
// in-memory backlog plus whatever is buffered in the worker channel. Exposed for
// health/monitoring so operators can see queue pressure.
func (w *CloneWorker) QueueDepth() int {
	w.pendingMu.Lock()
	backlog := len(w.pending)
	w.pendingMu.Unlock()
	return backlog + len(w.jobQueue)
}

// dispatch drains the overflow backlog into jobQueue, blocking on a full queue
// until a worker frees a slot (or the worker shuts down). Running as a single
// goroutine, it replaces the previous unbounded goroutine-per-job fallback.
func (w *CloneWorker) dispatch() {
	defer w.wg.Done()
	for {
		w.pendingMu.Lock()
		if len(w.pending) == 0 {
			w.pendingMu.Unlock()
			select {
			case <-w.notify:
				continue
			case <-w.softStop:
				return
			case <-w.ctx.Done():
				return
			}
		}
		job := w.pending[0]
		w.pending = w.pending[1:]
		// Release the backing array once fully drained so a past burst does not
		// pin a large slice for the process lifetime.
		if len(w.pending) == 0 {
			w.pending = nil
		}
		w.pendingMu.Unlock()

		select {
		case w.jobQueue <- job:
		case <-w.softStop:
			return
		case <-w.ctx.Done():
			return
		}
	}
}

// admit registers one job as in-flight unless a graceful stop has begun. It
// returns false when the worker is stopping, in which case the caller must not
// process the job (it is left "pending" for the next start to recover). The lock
// pairs with Stop(), which sets stopping before calling inflight.Wait(), so the
// Add here can never grow the WaitGroup counter from zero concurrently with that
// Wait. On success the caller owns exactly one inflight.Done().
func (w *CloneWorker) admit() bool {
	w.inflightMu.Lock()
	defer w.inflightMu.Unlock()
	if w.stopping {
		return false
	}
	w.inflight.Add(1)
	return true
}

// worker is a goroutine that processes clone jobs.
func (w *CloneWorker) worker(id int) {
	defer w.wg.Done()
	logger := w.logger.With(zap.Int("worker_id", id))
	logger.Info("worker started")

	for {
		select {
		case <-w.ctx.Done():
			logger.Info("worker stopped")
			return
		case <-w.softStop:
			// Graceful shutdown began: exit rather than pick up new work.
			logger.Info("worker stopped")
			return
		case job := <-w.jobQueue:
			if job == nil {
				continue
			}
			// Register the job as in-flight before processing. admit() returns false
			// if a graceful stop has begun (leaving the job "pending" for the next
			// start to recover) and, on success, takes the same lock Stop() holds
			// before inflight.Wait() — so the Add can never race that Wait.
			if !w.admit() {
				logger.Info("worker stopped")
				return
			}
			w.processJob(logger, job)
		}
	}
}

// cleanupStaleTempClones removes any leftover "<finalPath>.tmp-*" sibling
// directories from a previously interrupted atomic clone so they do not
// accumulate on disk. It reads the parent directory and matches by prefix rather
// than globbing to avoid filepath.Glob metacharacter issues in storage paths.
func cleanupStaleTempClones(logger *zap.Logger, finalPath string) {
	dir := filepath.Dir(finalPath)
	prefix := filepath.Base(finalPath) + ".tmp-"
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), prefix) {
			continue
		}
		p := filepath.Join(dir, e.Name())
		if rmErr := os.RemoveAll(p); rmErr != nil {
			logger.Warn("failed to remove stale temp clone directory", zap.String("path", p), zap.Error(rmErr))
		} else {
			logger.Info("removed stale temp clone directory", zap.String("path", p))
		}
	}
}

// processJob processes a single clone job.
func (w *CloneWorker) processJob(logger *zap.Logger, job *models.CloneJob) {
	// The caller (worker) already registered this job as in-flight via admit(), so a
	// graceful Stop() can wait for it within the shutdown grace period; release that
	// count when we return.
	defer w.inflight.Done()

	logger.Info("processing clone job", zap.String("job_id", job.ID.String()))

	// Get repository details first so we know the on-disk path to serialize on.
	var repo models.Repository
	if err := w.db.First(&repo, job.RepositoryID).Error; err != nil {
		w.failJob(logger, job, fmt.Sprintf("repository not found: %v", err))
		return
	}

	// StoragePath is persisted as the full on-disk path (base root already
	// joined at creation time), so it is used directly here.
	repoPath := repo.StoragePath

	// Never run two git operations against the same on-disk repository
	// concurrently. If another worker already holds this path (e.g. a manual
	// "clone now" collided with a scheduled sync, or a retry raced a manual
	// trigger), put the job back to pending and requeue it shortly rather than
	// blocking this worker slot for the duration of the other clone.
	unlock, ok := w.repoLocks.TryLock(repoPath)
	if !ok {
		logger.Info("repository already being processed, deferring job",
			zap.String("job_id", job.ID.String()), zap.String("path", repoPath))
		// Do not resurrect a job that was cancelled while queued: only defer it
		// back to pending when it is still deferrable, and skip the re-enqueue when
		// the conditional update matched nothing (RowsAffected == 0).
		deferred := w.db.Model(job).Where("status <> ?", "cancelled").Update("status", "pending")
		if deferred.Error != nil {
			// Still re-enqueue: the deferral itself only failed to persist, and the
			// retry re-runs the same conditional update.
			logger.Error("failed to defer clone job to pending",
				zap.String("job_id", job.ID.String()), zap.Error(deferred.Error))
		} else if deferred.RowsAffected == 0 {
			logger.Info("clone job was cancelled while queued, not re-enqueuing", zap.String("job_id", job.ID.String()))
			return
		}
		w.enqueueAfter(job, 5*time.Second)
		return
	}
	defer unlock()

	// Reflect in-flight work and remaining backlog in metrics.
	metrics.ActiveCloneJobs.Inc()
	defer func() {
		metrics.ActiveCloneJobs.Dec()
		metrics.CloneQueueDepth.Set(float64(w.QueueDepth()))
	}()

	// Update job status to running
	now := time.Now().UTC()
	w.db.Model(job).Updates(map[string]any{
		"status":     "running",
		"started_at": now,
		"output_log": "",
		"exit_code":  nil,
	})
	if started.Error != nil {
		// Proceed with the clone anyway: the status row is cosmetic relative to the
		// work itself, and the terminal-status update at the end retries the write.
		logger.Error("failed to mark clone job running",
			zap.String("job_id", job.ID.String()), zap.Error(started.Error))
	} else if started.RowsAffected == 0 {
		logger.Info("clone job was cancelled while queued, skipping", zap.String("job_id", job.ID.String()))
		return
	}

	// Decrypt credentials
	var creds *crypto.CredentialsPayload
	if repo.EncryptedCredentials != "" {
		var err error
		payload, err := w.encryption.Decrypt(repo.EncryptedCredentials)
		if err != nil {
			w.failJob(logger, job, fmt.Sprintf("failed to decrypt credentials: %v", err))
			return
		}
		creds = &payload
	}

	// Build clone options
	cloneOpts := &CloneOptions{
		URL:    repo.URL,
		Path:   repoPath,
		Branch: repo.Branch,
		Bare:   repo.IsBare,
		LFS:    repo.LFSEnabled,
		Dedupe: w.config.DedupeEnabled,
	}

	if creds != nil {
		cloneOpts.Username = creds.Username
		cloneOpts.Password = creds.Password
		cloneOpts.Token = creds.Token
		cloneOpts.SSHKey = creds.SSHKey
	}

	// Create progress callback
	progress := &CloneProgress{
		jobID:        job.ID,
		repositoryID: repo.ID.String(),
		db:           w.db,
	}
	cloneOpts.Progress = progress

	// Bound the whole git operation by the configured clone timeout so a hung
	// remote cannot pin a worker slot forever. Derived from w.ctx so a shutdown
	// still cancels it.
	opCtx := w.ctx
	if w.config.CloneTimeout > 0 {
		var cancel context.CancelFunc
		opCtx, cancel = context.WithTimeout(w.ctx, time.Duration(w.config.CloneTimeout)*time.Second)
		defer cancel()
	}

	// Perform clone or fetch. Decide based solely on whether a valid repository
	// already exists on disk — not on LastCloneAt. A repo can be present on disk
	// while LastCloneAt is still nil (duplicate queued jobs, or a prior clone
	// whose timestamp was not persisted); gating on LastCloneAt made those cases
	// attempt a fresh clone into a populated directory, which fails with
	// "repository already exists" and breaks periodic re-mirroring.
	var err error
	isUpdate := w.gitOps.IsRepositoryCloned(repoPath)

	if isUpdate {
		logger.Info("performing fetch", zap.String("repo", repo.Name))

		// Query references before fetch
		refsBefore, refsBeforeErr := w.gitOps.GetReferences(opCtx, repoPath)

		err = w.gitOps.FetchRepository(opCtx, cloneOpts)

		// If fetch succeeded, check for pruned branches
		if err == nil {
			refsAfter, refsAfterErr := w.gitOps.GetReferences(opCtx, repoPath)

			// Skip prune detection entirely if either snapshot failed: a nil
			// refsAfter would otherwise make every pre-fetch branch look deleted
			// and create bogus DeletedBranch records.
			if refsBeforeErr != nil || refsAfterErr != nil {
				logger.Warn("skipping pruned-branch detection due to ref listing error",
					zap.NamedError("refs_before_err", refsBeforeErr),
					zap.NamedError("refs_after_err", refsAfterErr))
				refsBefore = nil
			}

			// Find missing references that were branches. Protect each pruned
			// commit with a paperbin ref, then persist all DeletedBranch records in
			// a single transaction so a mid-loop failure cannot leave a partial set.
			var deletedBranches []models.DeletedBranch
			for refName, sha := range refsBefore {
				isBranch := strings.HasPrefix(refName, "refs/heads/") || strings.HasPrefix(refName, "refs/remotes/origin/")
				if isBranch {
					if _, exists := refsAfter[refName]; !exists {
						// Extract branch name
						branchName := strings.TrimPrefix(refName, "refs/heads/")
						branchName = strings.TrimPrefix(branchName, "refs/remotes/origin/")

						// Skip standard HEAD tracking reference
						if branchName == "HEAD" || strings.HasSuffix(branchName, "/HEAD") {
							continue
						}

						logger.Info("pruned branch detected (deleted online)", zap.String("branch", branchName), zap.String("sha", sha))

						// Create a paperbin ref in the local repo to keep the commit alive.
						paperbinRef := "refs/paperbin/heads/" + branchName
						_, updateErr := RunGitCommand(opCtx, repoPath, "update-ref", paperbinRef, sha)
						if updateErr != nil {
							// Without the paperbin ref the commit is not protected, so the
							// branch would not actually be restorable. Skip recording it.
							logger.Error("failed to create paperbin ref, skipping paperbin record", zap.String("ref", paperbinRef), zap.Error(updateErr))
							continue
						}

						deletedBranches = append(deletedBranches, models.DeletedBranch{
							ID:           uuid.New(),
							RepositoryID: repo.ID,
							BranchName:   branchName,
							CommitSHA:    sha,
							DeletedAt:    time.Now().UTC(),
						})
					}
				}
			}
			if len(deletedBranches) > 0 {
				if dbErr := w.db.Create(&deletedBranches).Error; dbErr != nil {
					logger.Error("failed to save deleted branches in DB", zap.Int("count", len(deletedBranches)), zap.Error(dbErr))
				}
			}
		}
	} else {
		// The path is not a valid repository but may still hold a leftover from a
		// previously interrupted clone (a legacy in-place partial), and there may be
		// stale *.tmp-* siblings from an interrupted atomic clone. git clone refuses
		// a non-empty destination, so clear both first so the clone can self-heal.
		if _, statErr := os.Stat(repoPath); statErr == nil {
			logger.Warn("removing leftover incomplete clone directory before re-clone", zap.String("path", repoPath))
			if rmErr := os.RemoveAll(repoPath); rmErr != nil {
				logger.Error("failed to remove leftover clone directory", zap.String("path", repoPath), zap.Error(rmErr))
			}
		}
		cleanupStaleTempClones(logger, repoPath)

		// Clone into a sibling temp directory on the same filesystem, then rename it
		// into place only after a successful clone. This guarantees repoPath only
		// ever appears as a complete repository: an interrupted attempt leaves a
		// *.tmp-* directory (reaped above and on the next attempt) instead of a
		// half-written final path that would make every future clone see a
		// non-empty destination and fail permanently. Git repositories are
		// relocatable, and dedupe's object "alternates" file holds an absolute path
		// to the shared pool, so the rename does not break either.
		tmpPath := repoPath + ".tmp-" + job.ID.String()
		if rmErr := os.RemoveAll(tmpPath); rmErr != nil {
			logger.Warn("failed to clear pre-existing temp clone dir", zap.String("path", tmpPath), zap.Error(rmErr))
		}

		logger.Info("performing clone", zap.String("repo", repo.Name))
		cloneOpts.Path = tmpPath
		err = w.gitOps.CloneRepository(opCtx, cloneOpts)
		cloneOpts.Path = repoPath

		if err == nil {
			// The destination must not exist for os.Rename on Windows (and to avoid
			// merging into a stray dir on Linux); we already removed any leftover.
			if rmErr := os.RemoveAll(repoPath); rmErr != nil {
				logger.Warn("failed to clear destination before rename", zap.String("path", repoPath), zap.Error(rmErr))
			}
			if renErr := os.Rename(tmpPath, repoPath); renErr != nil {
				err = fmt.Errorf("failed to move completed clone into place: %w", renErr)
				if rmErr := os.RemoveAll(tmpPath); rmErr != nil {
					logger.Warn("failed to remove temp clone after failed rename", zap.String("path", tmpPath), zap.Error(rmErr))
				}
			}
		} else {
			// Clean up the partial temp clone on failure.
			if rmErr := os.RemoveAll(tmpPath); rmErr != nil {
				logger.Warn("failed to remove temp clone after failed clone", zap.String("path", tmpPath), zap.Error(rmErr))
			}
		}
	}

	// If the worker is shutting down (w.ctx cancelled, not a per-op timeout), put
	// the job back to pending rather than marking it failed, so a restart resumes
	// it instead of surfacing a spurious failure.
	if err != nil && w.ctx.Err() != nil {
		logger.Info("clone interrupted by shutdown, requeuing job", zap.String("job_id", job.ID.String()))
		w.db.Model(job).Updates(map[string]any{"status": "pending", "started_at": nil})
		return
	}

	if err != nil {
		w.failJob(logger, job, fmt.Sprintf("git operation failed: %v", err))
		w.handleFailure(logger, repo, job, err)

		// Send webhook event for clone failure
		if w.webhookSender != nil {
			_ = w.webhookSender.SendEvent(w.ctx, "clone.failed", map[string]any{
				"repository_id":   repo.ID.String(),
				"repository_name": repo.Name,
				"job_id":          job.ID.String(),
				"error":           err.Error(),
				"timestamp":       time.Now().UTC().Format(time.RFC3339),
				"trigger_type":    job.TriggerType,
			})
		}

		// Send email notification for clone failure
		if w.emailSender != nil {
			_ = w.emailSender.SendCloneFailureNotification(w.ctx, &repo, job, err)
		}

		return
	}

	// Update job status to success
	w.db.Model(job).Updates(map[string]any{
		"status":           "success",
		"completed_at":     time.Now().UTC(),
		"progress_percent": 100,
		"output_log":       "Clone completed successfully",
	})

	// Post-clone housekeeping (gc, wiki mirror, GitHub metadata + tag sync, the
	// repository metadata refresh and the success webhook) lives in postclone.go
	// to keep this function focused on the clone/fetch itself.
	w.runPostClone(logger, job, repo, creds, progress, repoPath)

	// Persist the repository's on-disk size so the dashboard can SUM it from the DB
	// instead of walking the whole storage tree. Measured after housekeeping — git
	// gc --auto may have repacked the object store — so the stored figure reflects
	// the settled tree. Best-effort: skip persisting a zero (a transient walk
	// failure) rather than clobbering a previously good value on an already
	// successful job.
	if size := DirSize(repoPath); size > 0 {
		w.db.Model(&models.Repository{}).Where("id = ?", repo.ID).Update("size_bytes", size)
	}

	metrics.CloneJobsTotal.WithLabelValues("success", job.TriggerType).Inc()
	metrics.CloneJobDuration.Observe(time.Since(now).Seconds())

	logger.Info("clone job completed successfully", zap.String("job_id", job.ID.String()))
}

// failJob marks a job as failed.
func (w *CloneWorker) failJob(logger *zap.Logger, job *models.CloneJob, errMsg string) {
	logger.Error("job failed", zap.String("job_id", job.ID.String()), zap.String("error", errMsg))

	metrics.CloneJobsTotal.WithLabelValues("failed", job.TriggerType).Inc()

	w.db.Model(job).Updates(map[string]any{
		"status":       "failed",
		"completed_at": time.Now().UTC(),
		"output_log":   errMsg,
	})

	w.db.Model(&models.Repository{}).Where("id = ?", job.RepositoryID).Updates(map[string]any{
		"status":            "failed",
		"last_clone_status": "failed",
	})
}

// retryBackoffCap bounds the exponential retry backoff so a repository that has
// failed many times is still retried roughly hourly rather than being pushed out
// indefinitely.
const retryBackoffCap = time.Hour

// retryBackoff returns the delay before the next attempt for a repository that has
// already failed retryCount times: an exponential base*2^retryCount capped at
// retryBackoffCap, plus up to 50% jitter so many repositories failing together do
// not retry in lockstep (thundering herd).
func (w *CloneWorker) retryBackoff(retryCount int) time.Duration {
	base := w.config.RetryBaseDelay
	if base <= 0 {
		base = time.Minute
	}
	backoff := base
	for i := 0; i < retryCount && backoff < retryBackoffCap; i++ {
		backoff *= 2
	}
	if backoff > retryBackoffCap {
		backoff = retryBackoffCap
	}
	// Full-ish jitter: add a random fraction up to half the backoff.
	// #nosec G404 - retry-jitter timing is not security-sensitive; math/rand is fine.
	jitter := time.Duration(rand.Int64N(int64(backoff)/2 + 1))
	return backoff + jitter
}

// handleFailure applies retry backoff after a failed clone. It always records the
// exponential-with-jitter next-eligible time (next_retry_at) so the scheduler
// does not re-queue a hard-failing repository on every tick. Within the in-process
// attempt budget it also schedules a fast retry job; once the budget is spent it
// leaves further retries to the scheduler, which honors next_retry_at.
func (w *CloneWorker) handleFailure(logger *zap.Logger, repo models.Repository, _ *models.CloneJob, _ error) {
	var repoData models.Repository
	if err := w.db.First(&repoData, repo.ID).Error; err != nil {
		w.logger.Error("failed to load repository for retry backoff", zap.String("repo_id", repo.ID.String()), zap.Error(err))
		return
	}

	backoff := w.retryBackoff(repoData.RetryCount)
	nextRetryAt := time.Now().UTC().Add(backoff)

	// Persist the incremented retry count and the next-eligible time together so
	// the scheduler's backoff gate (next_retry_at) is always up to date, even once
	// in-process retries are exhausted.
	w.db.Model(&repoData).Updates(map[string]any{
		"retry_count":   repoData.RetryCount + 1,
		"next_retry_at": nextRetryAt,
	})

	logger.Warn("scheduling retry with backoff",
		zap.String("repo", repo.Name),
		zap.Int("retry", repoData.RetryCount+1),
		zap.Duration("backoff", backoff),
		zap.Time("next_retry_at", nextRetryAt),
	)

	if repoData.RetryCount >= w.config.RetryMaxAttempts {
		// In-process attempt budget spent; the scheduler will re-attempt after
		// next_retry_at, keeping a permanently-failing repo on the capped interval
		// instead of hammering the origin every tick.
		logger.Warn("in-process retry budget exhausted; scheduler will retry after backoff",
			zap.String("repo", repo.Name), zap.Time("next_retry_at", nextRetryAt))
		return
	}

	// Persist a real, PK-bearing job row before enqueuing. processJob updates the
	// row by primary key, so an in-memory job with no ID silently no-ops and the
	// retry never happens. Create it now (pending) so it is visible to the
	// scheduler's dedup and recoverable on restart, then dispatch it after the
	// backoff via a shutdown-aware, WaitGroup-tracked timer.
	retryJob := &models.CloneJob{
		ID:           uuid.New(),
		RepositoryID: repo.ID,
		TriggerType:  "scheduled",
		Status:       "pending",
		CreatedAt:    time.Now().UTC(),
	}
	if err := w.db.Create(retryJob).Error; err != nil {
		w.logger.Error("failed to persist retry clone job", zap.String("repo_id", repo.ID.String()), zap.Error(err))
		return
	}
	w.enqueueAfter(retryJob, backoff)
}

// progressDBInterval throttles how often clone progress is persisted to the DB.
// Git emits progress lines very frequently; writing every one caused heavy write
// amplification. Live subscribers still receive every line via the LogHub.
const progressDBInterval = 2 * time.Second

// CloneProgress implements git.Progress interface.
type CloneProgress struct {
	jobID        uuid.UUID
	repositoryID string
	db           *gorm.DB

	mu          sync.Mutex
	lastPersist time.Time
}

// Write implements the Write method for progress tracking. Every chunk is
// broadcast live, but the DB is updated at most once per progressDBInterval to
// avoid write amplification during large clones.
func (p *CloneProgress) Write(b []byte) (n int, err error) {
	message := string(b)
	GlobalLogHub.Broadcast(p.repositoryID, message)

	p.mu.Lock()
	persist := time.Since(p.lastPersist) >= progressDBInterval
	if persist {
		p.lastPersist = time.Now()
	}
	p.mu.Unlock()

	if persist {
		p.db.Model(&models.CloneJob{}).Where("id = ?", p.jobID).Update("output_log", message)
	}
	return len(b), nil
}
