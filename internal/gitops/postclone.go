package gitops

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/gitduppy/gitduppy/internal/models"
	"github.com/gitduppy/gitduppy/pkg/crypto"
	"github.com/gitduppy/gitduppy/pkg/validator"
	"github.com/google/uuid"
	"go.uber.org/zap"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// runPostClone performs the best-effort housekeeping that follows a successful
// clone/fetch: git gc, wiki mirroring, GitHub metadata + description/tag sync, the
// repository success bookkeeping, and the clone.success webhook. Every step is
// non-fatal (failures are logged, not surfaced) since the primary mirror already
// succeeded by the time this runs.
func (w *CloneWorker) runPostClone(logger *zap.Logger, job *models.CloneJob, repo models.Repository, creds *crypto.CredentialsPayload, progress *CloneProgress, repoPath string) {
	// Post-clone housekeeping gets its OWN timeout budget derived fresh from w.ctx,
	// rather than sharing the clone's context which the primary clone has already
	// partly (or fully) consumed. Otherwise a slow but successful clone would leave
	// no time for these follow-ups and they would silently skip.
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

	w.mirrorWiki(postCtx, logger, repo, creds, progress, repoPath)
	w.syncGitHubMetadata(postCtx, logger, repo, creds, repoPath)

	// Update repository. Reset the retry backoff state (retry_count and
	// next_retry_at) so a repo that recovers after failures is scheduled normally
	// again rather than staying under a stale backoff window.
	w.db.Model(&repo).Updates(map[string]any{
		"last_clone_at":     time.Now().UTC(),
		"last_clone_status": "success",
		"retry_count":       0,
		"next_retry_at":     nil,
		"status":            "success",
	})

	// Send webhook event for clone success
	if w.webhookSender != nil {
		_ = w.webhookSender.SendEvent(w.ctx, "clone.success", map[string]any{
			"repository_id":   repo.ID.String(),
			"repository_name": repo.Name,
			"job_id":          job.ID.String(),
			"timestamp":       time.Now().UTC().Format(time.RFC3339),
			"trigger_type":    job.TriggerType,
		})
	}
}

// mirrorWiki clones or fetches the repository's GitHub-style ".wiki" companion
// repository when wiki mirroring is enabled for it.
func (w *CloneWorker) mirrorWiki(ctx context.Context, logger *zap.Logger, repo models.Repository, creds *crypto.CredentialsPayload, progress *CloneProgress, repoPath string) {
	if !repo.MirrorWiki {
		return
	}

	wikiURL := repo.URL
	if strings.HasSuffix(wikiURL, ".git") {
		wikiURL = strings.TrimSuffix(wikiURL, ".git") + ".wiki.git"
	} else {
		wikiURL += ".wiki.git"
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
		wikiErr = w.gitOps.FetchRepository(ctx, wikiOpts)
	} else {
		// Clear any leftover incomplete wiki tree so the clone can self-heal.
		if _, statErr := os.Stat(wikiPath); statErr == nil {
			_ = os.RemoveAll(wikiPath)
		}
		logger.Info("performing wiki clone", zap.String("repo", repo.Name))
		wikiErr = w.gitOps.CloneRepository(ctx, wikiOpts)
	}
	if wikiErr != nil {
		logger.Warn("wiki clone/fetch failed", zap.Error(wikiErr))
	}
}

// syncGitHubMetadata archives GitHub-hosted issue/PR/release metadata (when
// enabled) and refreshes the repository's description, visibility and topic tags
// from the GitHub API.
func (w *CloneWorker) syncGitHubMetadata(ctx context.Context, logger *zap.Logger, repo models.Repository, creds *crypto.CredentialsPayload, repoPath string) {
	// Fetch GitHub Metadata if any are requested
	if repo.MirrorIssues || repo.MirrorPullRequests || repo.MirrorReleases {
		fetcher := NewGitHubMetadataFetcher()
		token := githubToken(creds, repo)
		if err := fetcher.FetchMetadata(ctx, repo.URL, repoPath, token, repo.MirrorIssues, repo.MirrorPullRequests, repo.MirrorReleases); err != nil {
			logger.Warn("failed to fetch github metadata", zap.Error(err))
		}
	}

	// Fetch description and tags for GitHub repositories
	if !strings.Contains(repo.URL, "github.com") {
		return
	}

	fetcher := NewGitHubMetadataFetcher()
	token := githubToken(creds, repo)
	info, err := fetcher.FetchRepositoryInfo(ctx, repo.URL, token)
	if err != nil {
		logger.Warn("failed to fetch github repo info", zap.Error(err))
		return
	}

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

	// Merge GitHub topics into the existing tag set instead of replacing it, so
	// manually-assigned tags (e.g. via TagIDs) are preserved across syncs. The
	// whole load-merge-replace runs in one transaction: if loading the current tags
	// fails we must abort — a partial view of the tags would drive a Replace that
	// wipes manual tags — and rolling back leaves the tags untouched. Skip entirely
	// when there are no usable topics.
	topics := sanitizeTopics(info.Topics)
	if len(topics) == 0 {
		return
	}

	// The tags table is global and admin-managed, while topics are chosen by
	// whoever controls the upstream repository. Topics are therefore only matched
	// against existing tags; new tags are created from them only when the
	// repository's owner is an admin (who could create those tags anyway).
	canCreateTags := w.repoOwnerIsAdmin(repo)

	if txErr := w.db.Transaction(func(tx *gorm.DB) error {
		var currentTags []models.Tag
		if err := tx.Model(&repo).Association("Tags").Find(&currentTags); err != nil {
			return fmt.Errorf("load existing tags: %w", err)
		}

		if canCreateTags {
			// Insert every topic as a tag in one batch, skipping names that
			// already exist (DO NOTHING on the unique name). One round-trip
			// instead of N FirstOrCreate calls, and race-safe against a concurrent
			// sync inserting the same topic. Color matches the model's gorm
			// default so worker- and UI-created tags agree.
			topicTags := make([]models.Tag, len(topics))
			for i, topic := range topics {
				topicTags[i] = models.Tag{ID: uuid.New(), Name: topic, Color: models.DefaultTagColor}
			}
			if err := tx.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "name"}},
				DoNothing: true,
			}).Create(&topicTags).Error; err != nil {
				return fmt.Errorf("insert topic tags: %w", err)
			}
		}

		// Select the canonical rows by name in a single query: with DoNothing the
		// conflicting rows come back without their real IDs, and a concurrent sync
		// may have inserted some, so trust the table rather than the insert result.
		// Without tag-creation rights this simply picks the topics that already
		// exist as tags.
		var canonical []models.Tag
		if err := tx.Where("name IN ?", topics).Find(&canonical).Error; err != nil {
			return fmt.Errorf("load canonical topic tags: %w", err)
		}
		if len(canonical) == 0 {
			return nil
		}

		tagSet := make(map[uuid.UUID]models.Tag, len(currentTags)+len(canonical))
		for _, t := range currentTags {
			tagSet[t.ID] = t
		}
		for _, t := range canonical {
			tagSet[t.ID] = t
		}
		merged := make([]models.Tag, 0, len(tagSet))
		for _, t := range tagSet {
			merged = append(merged, t)
		}
		return tx.Model(&repo).Association("Tags").Replace(merged)
	}); txErr != nil {
		logger.Error("Failed to sync repository tags, skipping to avoid wiping manual tags", zap.Error(txErr))
	}
}

// sanitizeTopics keeps the upstream topics that are valid tag names (the same
// rule the tag API enforces), de-duplicated.
func sanitizeTopics(topics []string) []string {
	seen := make(map[string]struct{}, len(topics))
	out := make([]string, 0, len(topics))
	for _, t := range topics {
		if !validator.ValidateTagName(t) {
			continue
		}
		if _, dup := seen[t]; dup {
			continue
		}
		seen[t] = struct{}{}
		out = append(out, t)
	}
	return out
}

// repoOwnerIsAdmin reports whether the repository's creator is an admin.
// Repositories without a recorded creator, or whose creator cannot be loaded,
// are treated as non-admin.
func (w *CloneWorker) repoOwnerIsAdmin(repo models.Repository) bool {
	if repo.CreatedBy == nil {
		return false
	}
	var owner models.User
	if err := w.db.Select("id", "role").First(&owner, "id = ?", *repo.CreatedBy).Error; err != nil {
		return false
	}
	return owner.IsAdmin()
}

// githubToken picks the credential usable as a GitHub API token: an explicit
// token, or an HTTPS password used as one.
func githubToken(creds *crypto.CredentialsPayload, repo models.Repository) string {
	switch {
	case creds == nil:
		return ""
	case creds.Token != "":
		return creds.Token
	case creds.Password != "" && repo.AuthType == "https":
		return creds.Password
	default:
		return ""
	}
}
