package gitops

import (
	"context"
	"fmt"
	"os"
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

// Unsubscribe removes a channel subscription.
func (h *LogHub) Unsubscribe(repoID string, ch chan string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	subs := h.subscribers[repoID]
	for i, sub := range subs {
		if sub == ch {
			h.subscribers[repoID] = append(subs[:i], subs[i+1:]...)
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

	// Overflow backlog drained by a single dispatcher goroutine so that a burst
	// larger than jobQueue's buffer never spawns a goroutine per job.
	pendingMu sync.Mutex
	pending   []*models.CloneJob
	notify    chan struct{}
}

// WebhookSender interface for sending webhook events.
type WebhookSender interface {
	SendEvent(ctx context.Context, eventType string, payload map[string]interface{}) error
}

// EmailSender interface for sending email notifications.
type EmailSender interface {
	SendCloneFailureNotification(ctx context.Context, repo *models.Repository, job *models.CloneJob, err error) error
}

// NewCloneWorker creates a new clone worker.
func NewCloneWorker(config *WorkerConfig, gitOps *GitOperations, encryption *crypto.EncryptionService) *CloneWorker {
	ctx, cancel := context.WithCancel(context.Background())
	return &CloneWorker{
		config:     config,
		gitOps:     gitOps,
		db:         database.GetDB(),
		encryption: encryption,
		jobQueue:   make(chan *models.CloneJob, 128),
		notify:     make(chan struct{}, 1),
		ctx:        ctx,
		cancel:     cancel,
		logger:     zap.L().Named("clone-worker"),
	}
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

	// Requeue any jobs left pending or interrupted mid-run by a previous restart
	// so they are not stranded. Only *stale* running jobs are reset (see
	// requeueUnfinishedJobs) so a co-running instance's in-flight jobs are left
	// alone under a multi-replica deployment.
	w.requeueUnfinishedJobs()
}

// requeueUnfinishedJobs re-enqueues jobs that never completed (pending, or
// "running" long enough that no live worker could still hold them) so a restart
// or a burst does not strand them.
//
// A "running" job is only reset when its started_at is older than a stale
// threshold derived from the clone timeout. This avoids clobbering jobs that
// another replica started moments ago — resetting those would let two instances
// clone into the same directory concurrently.
func (w *CloneWorker) requeueUnfinishedJobs() {
	if w.db == nil {
		return
	}

	// Stale = older than the clone timeout plus a margin; a running job past this
	// point cannot still be owned by a live worker (which would have timed out).
	timeout := time.Duration(w.config.CloneTimeout) * time.Second
	if timeout <= 0 {
		timeout = time.Duration(DefaultWorkerConfig().CloneTimeout) * time.Second
	}
	staleBefore := time.Now().Add(-timeout - time.Minute)

	// Rows whose started_at is NULL are also treated as stale: they were marked
	// running but never recorded a start, so no worker is holding them.
	w.db.Model(&models.CloneJob{}).
		Where("status = ? AND (started_at IS NULL OR started_at < ?)", "running", staleBefore).
		Update("status", "pending")

	var jobs []models.CloneJob
	if err := w.db.Where("status = ?", "pending").Order("created_at asc").Find(&jobs).Error; err != nil {
		w.logger.Error("failed to load pending jobs for requeue", zap.Error(err))
		return
	}
	if len(jobs) == 0 {
		return
	}
	w.logger.Info("requeuing unfinished clone jobs", zap.Int("count", len(jobs)))
	for i := range jobs {
		w.Enqueue(&jobs[i])
	}
}

// Stop stops the worker pool. The job channel is intentionally NOT closed: with
// the non-dropping Enqueue below, closing it could race a blocked send and panic.
func (w *CloneWorker) Stop() {
	w.logger.Info("stopping clone worker pool")
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
		case <-w.ctx.Done():
			return
		}
	}
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
		case job := <-w.jobQueue:
			if job == nil {
				continue
			}
			w.processJob(logger, job)
		}
	}
}

// processJob processes a single clone job.
func (w *CloneWorker) processJob(logger *zap.Logger, job *models.CloneJob) {
	logger.Info("processing clone job", zap.String("job_id", job.ID.String()))

	// Reflect in-flight work and remaining backlog in metrics.
	metrics.ActiveCloneJobs.Inc()
	defer func() {
		metrics.ActiveCloneJobs.Dec()
		metrics.CloneQueueDepth.Set(float64(w.QueueDepth()))
	}()

	// Update job status to running
	now := time.Now()
	w.db.Model(job).Updates(map[string]interface{}{
		"status":     "running",
		"started_at": now,
		"output_log": "",
		"exit_code":  nil,
	})

	// Get repository details
	var repo models.Repository
	if err := w.db.First(&repo, job.RepositoryID).Error; err != nil {
		w.failJob(logger, job, fmt.Sprintf("repository not found: %v", err))
		return
	}

	// StoragePath is persisted as the full on-disk path (base root already
	// joined at creation time), so it is used directly here.
	repoPath := repo.StoragePath

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
							DeletedAt:    time.Now(),
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
		// previously interrupted clone (a non-.git directory, or a half-written
		// tree). git clone refuses a non-empty destination, so that debris would
		// make every future attempt fail permanently. Clear it first so the clone
		// can self-heal.
		if _, statErr := os.Stat(repoPath); statErr == nil {
			logger.Warn("removing leftover incomplete clone directory before re-clone", zap.String("path", repoPath))
			if rmErr := os.RemoveAll(repoPath); rmErr != nil {
				logger.Error("failed to remove leftover clone directory", zap.String("path", repoPath), zap.Error(rmErr))
			}
		}
		logger.Info("performing clone", zap.String("repo", repo.Name))
		err = w.gitOps.CloneRepository(opCtx, cloneOpts)
	}

	// If the worker is shutting down (w.ctx cancelled, not a per-op timeout), put
	// the job back to pending rather than marking it failed, so a restart resumes
	// it instead of surfacing a spurious failure.
	if err != nil && w.ctx.Err() != nil {
		logger.Info("clone interrupted by shutdown, requeuing job", zap.String("job_id", job.ID.String()))
		w.db.Model(job).Updates(map[string]interface{}{"status": "pending", "started_at": nil})
		return
	}

	if err != nil {
		w.failJob(logger, job, fmt.Sprintf("git operation failed: %v", err))
		w.handleFailure(logger, repo, job, err)

		// Send webhook event for clone failure
		if w.webhookSender != nil {
			_ = w.webhookSender.SendEvent(w.ctx, "clone.failed", map[string]interface{}{
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
	w.db.Model(job).Updates(map[string]interface{}{
		"status":           "success",
		"completed_at":     time.Now(),
		"progress_percent": 100,
		"output_log":       "Clone completed successfully",
	})

	// Post-clone housekeeping (gc, wiki mirror, GitHub metadata) gets its OWN
	// timeout budget derived fresh from w.ctx, rather than sharing opCtx which the
	// primary clone has already partly (or fully) consumed. Otherwise a slow but
	// successful clone would leave no time for these follow-ups and they would
	// silently skip.
	postCtx := w.ctx
	if w.config.CloneTimeout > 0 {
		var postCancel context.CancelFunc
		postCtx, postCancel = context.WithTimeout(w.ctx, time.Duration(w.config.CloneTimeout)*time.Second)
		defer postCancel()
	}

	// Housekeeping: let git repack/prune loose objects when its own heuristics say
	// it is worthwhile. "--auto" is a cheap no-op until thresholds are crossed, so
	// this keeps long-lived mirrors compact without adding a heavy fixed cost.
	if _, gcErr := RunGitCommand(postCtx, repoPath, "gc", "--auto"); gcErr != nil {
		logger.Debug("git gc --auto skipped", zap.String("repo", repo.Name), zap.Error(gcErr))
	}

	// Mirror Wiki if requested
	if repo.MirrorWiki {
		wikiURL := repo.URL
		if strings.HasSuffix(wikiURL, ".git") {
			wikiURL = strings.TrimSuffix(wikiURL, ".git") + ".wiki.git"
		} else {
			wikiURL = wikiURL + ".wiki.git"
		}

		wikiPath := repoPath + ".wiki"

		wikiOpts := &CloneOptions{
			URL:  wikiURL,
			Path: wikiPath,
			Bare: repo.IsBare,
			LFS:  false,
		}
		if creds != nil {
			wikiOpts.Username = creds.Username
			wikiOpts.Password = creds.Password
			wikiOpts.Token = creds.Token
			wikiOpts.SSHKey = creds.SSHKey
		}
		wikiOpts.Progress = progress

		// Decide fetch vs clone from what is actually on disk (consistent with the
		// main tree), not from LastCloneAt which can be nil for an already-present
		// wiki and would force a clone into a populated directory.
		var wikiErr error
		if w.gitOps.IsRepositoryCloned(wikiPath) {
			logger.Info("performing wiki fetch", zap.String("repo", repo.Name))
			wikiErr = w.gitOps.FetchRepository(postCtx, wikiOpts)
		} else {
			// Clear any leftover incomplete wiki tree so the clone can self-heal.
			if _, statErr := os.Stat(wikiPath); statErr == nil {
				_ = os.RemoveAll(wikiPath)
			}
			logger.Info("performing wiki clone", zap.String("repo", repo.Name))
			wikiErr = w.gitOps.CloneRepository(postCtx, wikiOpts)
		}
		if wikiErr != nil {
			logger.Warn("wiki clone/fetch failed", zap.Error(wikiErr))
		}
	}

	// Fetch GitHub Metadata if any are requested
	if repo.MirrorIssues || repo.MirrorPullRequests || repo.MirrorReleases {
		fetcher := NewGitHubMetadataFetcher()
		token := ""
		if creds != nil && creds.Token != "" {
			token = creds.Token
		} else if creds != nil && creds.Password != "" && repo.AuthType == "basic" {
			token = creds.Password
		}

		if err := fetcher.FetchMetadata(postCtx, repo.URL, repoPath, token, repo.MirrorIssues, repo.MirrorPullRequests, repo.MirrorReleases); err != nil {
			logger.Warn("failed to fetch github metadata", zap.Error(err))
		}
	}

	// Fetch description and tags for GitHub repositories
	if strings.Contains(repo.URL, "github.com") {
		fetcher := NewGitHubMetadataFetcher()
		token := ""
		if creds != nil && creds.Token != "" {
			token = creds.Token
		} else if creds != nil && creds.Password != "" && repo.AuthType == "basic" {
			token = creds.Password
		}

		info, err := fetcher.FetchRepositoryInfo(postCtx, repo.URL, token)
		if err != nil {
			logger.Warn("failed to fetch github repo info", zap.Error(err))
		} else {
			// Update description
			if info.Description != "" {
				repo.Description = &info.Description
				w.db.Model(&repo).Update("description", info.Description)
			}

			// Update visibility (public/private)
			if info.Visibility != "" {
				repo.Visibility = info.Visibility
				w.db.Model(&repo).Update("visibility", info.Visibility)
			}

			// Merge GitHub topics into the existing tag set instead of
			// replacing it, so manually-assigned tags (e.g. via TagIDs) are
			// preserved across syncs. Only run the merge-and-Replace when the
			// existing tags were loaded successfully — otherwise currentTags is
			// incomplete and the Replace would wipe manual tags.
			var currentTags []models.Tag
			if err := w.db.Model(&repo).Association("Tags").Find(&currentTags); err != nil {
				logger.Error("Failed to load existing repository tags, skipping tag sync to avoid wiping manual tags", zap.Error(err))
			} else {
				tagSet := make(map[uuid.UUID]models.Tag)
				for _, t := range currentTags {
					tagSet[t.ID] = t
				}
				for _, topic := range info.Topics {
					var tag models.Tag
					// Use Attrs for the default color so it is applied ONLY on
					// insert. Passing it as a create struct would fold color into
					// the lookup WHERE, so a topic matching an existing tag with a
					// different color would miss, attempt a duplicate-name insert,
					// and drop the topic.
					if err := w.db.Where(models.Tag{Name: topic}).
						Attrs(models.Tag{Color: "#000000"}).
						FirstOrCreate(&tag).Error; err == nil {
						tagSet[tag.ID] = tag
					}
				}
				merged := make([]models.Tag, 0, len(tagSet))
				for _, t := range tagSet {
					merged = append(merged, t)
				}
				if err := w.db.Model(&repo).Association("Tags").Replace(merged); err != nil {
					logger.Error("Failed to sync repository tags", zap.Error(err))
				}
			}
		}
	}

	// Update repository
	w.db.Model(&repo).Updates(map[string]interface{}{
		"last_clone_at":     time.Now(),
		"last_clone_status": "success",
		"retry_count":       0,
		"status":            "success",
	})

	// Send webhook event for clone success
	if w.webhookSender != nil {
		_ = w.webhookSender.SendEvent(w.ctx, "clone.success", map[string]interface{}{
			"repository_id":   repo.ID.String(),
			"repository_name": repo.Name,
			"job_id":          job.ID.String(),
			"timestamp":       time.Now().UTC().Format(time.RFC3339),
			"trigger_type":    job.TriggerType,
		})
	}

	metrics.CloneJobsTotal.WithLabelValues("success", job.TriggerType).Inc()
	metrics.CloneJobDuration.Observe(time.Since(now).Seconds())

	logger.Info("clone job completed successfully", zap.String("job_id", job.ID.String()))
}

// failJob marks a job as failed.
func (w *CloneWorker) failJob(logger *zap.Logger, job *models.CloneJob, errMsg string) {
	logger.Error("job failed", zap.String("job_id", job.ID.String()), zap.String("error", errMsg))

	metrics.CloneJobsTotal.WithLabelValues("failed", job.TriggerType).Inc()

	w.db.Model(job).Updates(map[string]interface{}{
		"status":       "failed",
		"completed_at": time.Now(),
		"output_log":   errMsg,
	})

	w.db.Model(&models.Repository{}).Where("id = ?", job.RepositoryID).Updates(map[string]interface{}{
		"status":            "failed",
		"last_clone_status": "failed",
	})
}

// handleFailure handles job failure with retry logic.
func (w *CloneWorker) handleFailure(logger *zap.Logger, repo models.Repository, _ *models.CloneJob, _ error) {
	// Increment retry count
	var repoData models.Repository
	w.db.First(&repoData, repo.ID)

	if repoData.RetryCount < w.config.RetryMaxAttempts {
		// Schedule retry with exponential backoff
		backoff := time.Duration(repoData.RetryCount+1) * w.config.RetryBaseDelay
		w.db.Model(&repoData).Update("retry_count", repoData.RetryCount+1)

		logger.Warn("scheduling retry",
			zap.String("repo", repo.Name),
			zap.Int("retry", repoData.RetryCount+1),
			zap.Duration("backoff", backoff),
		)

		repoID := repo.ID
		time.AfterFunc(backoff, func() {
			// Persist a real, PK-bearing job row before enqueuing. processJob
			// updates the row by primary key, so an in-memory job with no ID
			// (as before) silently no-ops and the retry never happens.
			retryJob := &models.CloneJob{
				ID:           uuid.New(),
				RepositoryID: repoID,
				TriggerType:  "scheduled",
				Status:       "pending",
				CreatedAt:    time.Now(),
			}
			if err := w.db.Create(retryJob).Error; err != nil {
				w.logger.Error("failed to persist retry clone job", zap.String("repo_id", repoID.String()), zap.Error(err))
				return
			}
			w.Enqueue(retryJob)
		})
	}
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

// Scheduler handles scheduled clone jobs.
type Scheduler struct {
	db     *gorm.DB
	worker *CloneWorker
	ticker *time.Ticker
	done   chan bool
	logger *zap.Logger
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
		done:   make(chan bool),
		logger: zap.L().Named("scheduler"),
	}
}

// Start starts the scheduler.
func (s *Scheduler) Start() {
	s.logger.Info("starting scheduler")
	go s.run()
}

// Stop stops the scheduler.
func (s *Scheduler) Stop() {
	s.logger.Info("stopping scheduler")
	s.ticker.Stop()
	s.done <- true
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
	// several ticks).
	var busyIDs []uuid.UUID
	s.db.Model(&models.CloneJob{}).
		Where("status IN ?", []string{"pending", "running"}).
		Distinct().
		Pluck("repository_id", &busyIDs)
	busy := make(map[uuid.UUID]struct{}, len(busyIDs))
	for _, id := range busyIDs {
		busy[id] = struct{}{}
	}

	now := time.Now()
	for _, repo := range repos {
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
				CreatedAt:    time.Now(),
			}
			s.db.Create(job)
			s.worker.Enqueue(job)
			s.logger.Debug("enqueued clone job", zap.String("repo", repo.Name))
		}
	}
}
