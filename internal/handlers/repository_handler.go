package handlers

import (
	"context"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gitduppy/gitduppy/internal/database"
	"github.com/gitduppy/gitduppy/internal/gitops"
	"github.com/gitduppy/gitduppy/internal/middleware"
	"github.com/gitduppy/gitduppy/internal/models"
	"github.com/gitduppy/gitduppy/internal/services"
	"github.com/gitduppy/gitduppy/pkg/crypto"
	"github.com/gitduppy/gitduppy/pkg/response"
	"github.com/gitduppy/gitduppy/pkg/validator"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

const stringTrue = "true"

// RepositoryHandler handles repository management requests.
type RepositoryHandler struct {
	repoService  *services.RepositoryService
	cloneService *services.CloneService
	tagService   *services.TagService
	auditService *services.AuditService
}

// NewRepositoryHandler creates a new repository handler.
func NewRepositoryHandler(
	repoService *services.RepositoryService,
	cloneService *services.CloneService,
	tagService *services.TagService,
	auditService *services.AuditService,
) *RepositoryHandler { //nolint:whitespace
	return &RepositoryHandler{
		repoService:  repoService,
		cloneService: cloneService,
		tagService:   tagService,
		auditService: auditService,
	}
}

// ListRepositories handles GET /api/v1/repositories.
func (h *RepositoryHandler) ListRepositories(c *gin.Context) {
	filter := &services.RepositoryFilter{
		Page:    1,
		PerPage: 20,
	}

	if page := c.Query("page"); page != "" {
		filter.Page = validator.ParseInt(page, 1)
	}
	if perPage := c.Query("per_page"); perPage != "" {
		filter.PerPage = validator.ParseInt(perPage, 20)
	}

	status := c.Query("status")
	if status != "" {
		filter.Status = &status
	}

	tag := c.Query("tag")
	if tag != "" {
		filter.Tag = &tag
	}

	search := c.Query("search")
	if search != "" {
		filter.Search = search
	}

	isActive := c.Query("is_active")
	if isActive != "" {
		active := isActive == stringTrue
		filter.IsActive = &active
	}

	sort := c.Query("sort")
	if sort != "" {
		filter.Sort = sort
	}

	repos, total, err := h.repoService.ListRepositories(c, filter)
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}

	response.SuccessWithMeta(c, repos, &response.Meta{
		Page:       filter.Page,
		PerPage:    filter.PerPage,
		Total:      int(total),
		TotalPages: int((total + int64(filter.PerPage) - 1) / int64(filter.PerPage)),
	})
}

// GetRepository handles GET /api/v1/repositories/:id.
func (h *RepositoryHandler) GetRepository(c *gin.Context) {
	id, ok := parseUUIDParam(c, "id", "repository")
	if !ok {
		return
	}

	repo, err := h.repoService.GetRepositoryByID(c, id)
	if err != nil {
		response.NotFound(c, "Repository not found")
		return
	}

	response.Success(c, repo)
}

// CreateRepository handles POST /api/v1/repositories.
func (h *RepositoryHandler) CreateRepository(c *gin.Context) {
	user, ok := middleware.GetCurrentUser(c)
	if !ok {
		response.Unauthorized(c, "Not authenticated")
		return
	}

	var req struct {
		Name                 string                     `json:"name" validate:"required"`
		URL                  string                     `json:"url" validate:"required"`
		Branch               string                     `json:"branch" validate:"required"`
		AuthType             string                     `json:"auth_type" validate:"required,oneof=none https ssh token"`
		Credentials          *crypto.CredentialsPayload `json:"credentials,omitempty"`
		IsBare               bool                       `json:"is_bare"`
		LFSEnabled           bool                       `json:"lfs_enabled"`
		MirrorIssues         bool                       `json:"mirror_issues"`
		MirrorPullRequests   bool                       `json:"mirror_pull_requests"`
		MirrorReleases       bool                       `json:"mirror_releases"`
		MirrorWiki           bool                       `json:"mirror_wiki"`
		CloneIntervalMinutes int                        `json:"clone_interval_minutes" validate:"min=5"`
		RetentionDays        int                        `json:"retention_days"`
		Description          *string                    `json:"description,omitempty"`
		TagIDs               []uuid.UUID                `json:"tag_ids,omitempty"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "INVALID_REQUEST", err.Error())
		return
	}

	if err := validator.ValidateStruct(&req); err != nil {
		response.BadRequest(c, "VALIDATION_ERROR", err.Error())
		return
	}

	if !validator.ValidateGitURL(req.URL) {
		response.BadRequest(c, "INVALID_URL", "Invalid git repository URL")
		return
	}

	repo, err := h.repoService.CreateRepository(c, &services.CreateRepositoryRequest{
		Name:                 req.Name,
		URL:                  req.URL,
		Branch:               req.Branch,
		AuthType:             req.AuthType,
		Credentials:          req.Credentials,
		IsBare:               req.IsBare,
		LFSEnabled:           req.LFSEnabled,
		MirrorIssues:         req.MirrorIssues,
		MirrorPullRequests:   req.MirrorPullRequests,
		MirrorReleases:       req.MirrorReleases,
		MirrorWiki:           req.MirrorWiki,
		CloneIntervalMinutes: req.CloneIntervalMinutes,
		RetentionDays:        req.RetentionDays,
		Description:          req.Description,
		TagIDs:               req.TagIDs,
	}, user.ID)
	if err != nil {
		response.BadRequest(c, "CREATE_ERROR", err.Error())
		return
	}

	// Log the action.
	if err := h.auditService.Log(c, &user.ID, &repo.ID, "repository.create", gin.H{"name": repo.Name}, c.ClientIP(), c.Request.UserAgent()); err != nil {
		log.Printf("WARNING: audit log failed for repository.create repo=%s: %v", repo.ID, err)
	}

	response.Created(c, repo)
}

// UpdateRepository handles PUT /api/v1/repositories/:id.
func (h *RepositoryHandler) UpdateRepository(c *gin.Context) {
	id, ok := parseUUIDParam(c, "id", "repository")
	if !ok {
		return
	}

	var req services.UpdateRepositoryRequest
	if bindErr := c.ShouldBindJSON(&req); bindErr != nil {
		response.BadRequest(c, "INVALID_REQUEST", bindErr.Error())
		return
	}

	repo, err := h.repoService.UpdateRepository(c, id, &req)
	if err != nil {
		response.BadRequest(c, "UPDATE_ERROR", err.Error())
		return
	}

	user, _ := middleware.GetCurrentUser(c)
	if user != nil {
		if err := h.auditService.Log(c, &user.ID, &repo.ID, "repository.update", gin.H{"name": repo.Name}, c.ClientIP(), c.Request.UserAgent()); err != nil {
			log.Printf("WARNING: audit log failed for repository.update repo=%s: %v", repo.ID, err)
		}
	}

	response.Success(c, repo)
}

// DeleteRepository handles DELETE /api/v1/repositories/:id.
func (h *RepositoryHandler) DeleteRepository(c *gin.Context) {
	id, ok := parseUUIDParam(c, "id", "repository")
	if !ok {
		return
	}

	repo, _ := h.repoService.GetRepositoryByID(c, id)

	if deleteErr := h.repoService.DeleteRepository(c, id); deleteErr != nil {
		response.BadRequest(c, "DELETE_ERROR", deleteErr.Error())
		return
	}

	user, _ := middleware.GetCurrentUser(c)
	if user != nil && repo != nil {
		if err := h.auditService.Log(c, &user.ID, &repo.ID, "repository.delete", gin.H{"name": repo.Name}, c.ClientIP(), c.Request.UserAgent()); err != nil {
			log.Printf("WARNING: audit log failed for repository.delete repo=%s: %v", repo.ID, err)
		}
	}

	response.SuccessWithMessage(c, "Repository deleted successfully", nil)
}

// SetRepositoryStatus handles PATCH /api/v1/repositories/:id/status.
func (h *RepositoryHandler) SetRepositoryStatus(c *gin.Context) {
	id, ok := parseUUIDParam(c, "id", "repository")
	if !ok {
		return
	}

	var req struct {
		IsActive bool `json:"is_active"`
	}
	if bindErr := c.ShouldBindJSON(&req); bindErr != nil {
		response.BadRequest(c, "INVALID_REQUEST", bindErr.Error())
		return
	}

	if statusErr := h.repoService.SetRepositoryStatus(c, id, req.IsActive); statusErr != nil {
		response.BadRequest(c, "UPDATE_ERROR", statusErr.Error())
		return
	}

	response.SuccessWithMessage(c, "Repository status updated", nil)
}

// TriggerClone handles POST /api/v1/repositories/:id/clone.
func (h *RepositoryHandler) TriggerClone(c *gin.Context) {
	id, ok := parseUUIDParam(c, "id", "repository")
	if !ok {
		return
	}

	job, err := h.cloneService.CreateCloneJob(c, id, "manual")
	if err != nil {
		response.BadRequest(c, "CREATE_ERROR", err.Error())
		return
	}

	response.Accepted(c, gin.H{
		"job_id":  job.ID,
		"status":  job.Status,
		"message": "Clone job queued",
	})
}

// GetRepositoryLogs handles GET /api/v1/repositories/:id/logs.
func (h *RepositoryHandler) GetRepositoryLogs(c *gin.Context) {
	id, ok := parseUUIDParam(c, "id", "repository")
	if !ok {
		return
	}

	limit := validator.ParseInt(c.Query("limit"), 100)
	logs, err := h.cloneService.GetRepositoryLogs(c, id, limit)
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}

	response.Success(c, logs)
}

// GetPaperbin handles GET /api/v1/repositories/paperbin.
func (h *RepositoryHandler) GetPaperbin(c *gin.Context) {
	db := database.GetDB()

	// 1. Fetch soft-deleted repositories
	var repos []models.Repository
	if err := db.Unscoped().Where("deleted_at IS NOT NULL").Find(&repos).Error; err != nil {
		response.InternalError(c, "Failed to fetch deleted repositories: "+err.Error())
		return
	}

	// 2. Fetch deleted branches
	var branches []models.DeletedBranch
	if err := db.Unscoped().Preload("Repository").Find(&branches).Error; err != nil {
		response.InternalError(c, "Failed to fetch deleted branches: "+err.Error())
		return
	}

	response.Success(c, gin.H{
		"repositories": repos,
		"branches":     branches,
	})
}

// RestoreRepository handles POST /api/v1/repositories/:id/restore.
func (h *RepositoryHandler) RestoreRepository(c *gin.Context) {
	id, ok := parseUUIDParam(c, "id", "repository")
	if !ok {
		return
	}

	if err := h.repoService.RestoreRepository(c, id); err != nil {
		response.BadRequest(c, "RESTORE_ERROR", err.Error())
		return
	}

	// Fetch restored repo to log and return
	repo, _ := h.repoService.GetRepositoryByID(c, id)

	user, _ := middleware.GetCurrentUser(c)
	if user != nil && repo != nil {
		if err := h.auditService.Log(c, &user.ID, &repo.ID, "repository.restore", gin.H{"name": repo.Name}, c.ClientIP(), c.Request.UserAgent()); err != nil {
			log.Printf("WARNING: audit log failed for repository.restore repo=%s: %v", repo.ID, err)
		}
	}

	response.SuccessWithMessage(c, "Repository restored successfully", repo)
}

// PermanentDeleteRepository handles DELETE /api/v1/repositories/:id/force.
func (h *RepositoryHandler) PermanentDeleteRepository(c *gin.Context) {
	id, ok := parseUUIDParam(c, "id", "repository")
	if !ok {
		return
	}

	db := database.GetDB()
	var repo models.Repository
	if err := db.Unscoped().First(&repo, id).Error; err != nil {
		response.NotFound(c, "Repository not found")
		return
	}

	if err := h.repoService.PermanentDeleteRepository(c, id); err != nil {
		response.BadRequest(c, "DELETE_ERROR", err.Error())
		return
	}

	user, _ := middleware.GetCurrentUser(c)
	if user != nil {
		if err := h.auditService.Log(c, &user.ID, nil, "repository.permanent_delete", gin.H{"id": id, "name": repo.Name}, c.ClientIP(), c.Request.UserAgent()); err != nil {
			log.Printf("WARNING: audit log failed for repository.permanent_delete: %v", err)
		}
	}

	response.SuccessWithMessage(c, "Repository permanently deleted", nil)
}

// RestoreBranch handles POST /api/v1/repositories/:id/paperbin/branches/:branchId/restore.
func (h *RepositoryHandler) RestoreBranch(c *gin.Context) {
	repoID, ok := parseUUIDParam(c, "id", "repository")
	if !ok {
		return
	}

	branchID, ok := parseUUIDParam(c, "branchId", "branch")
	if !ok {
		return
	}

	db := database.GetDB()

	var delBranch models.DeletedBranch
	if err := db.First(&delBranch, branchID).Error; err != nil {
		response.NotFound(c, "Deleted branch record not found")
		return
	}

	if delBranch.RepositoryID != repoID {
		response.BadRequest(c, "MISMATCH", "Branch does not belong to this repository")
		return
	}

	repo, err := h.repoService.GetRepositoryByID(c, repoID)
	if err != nil {
		response.NotFound(c, "Active repository not found")
		return
	}

	paperbinRef := "refs/paperbin/heads/" + delBranch.BranchName

	// Recreate branch
	_, gitErr := gitops.RunGitCommand(c, repo.StoragePath, "branch", delBranch.BranchName, delBranch.CommitSHA)
	if gitErr != nil {
		_, gitErr = gitops.RunGitCommand(c, repo.StoragePath, "branch", "-f", delBranch.BranchName, delBranch.CommitSHA)
	}

	if gitErr != nil {
		response.InternalError(c, "Failed to recreate git branch: "+gitErr.Error())
		return
	}

	// Delete the paperbin ref
	_, _ = gitops.RunGitCommand(c, repo.StoragePath, "update-ref", "-d", paperbinRef)

	// Delete DeletedBranch record
	if err := db.Delete(&delBranch).Error; err != nil {
		response.InternalError(c, "Failed to delete paperbin DB record: "+err.Error())
		return
	}

	user, _ := middleware.GetCurrentUser(c)
	if user != nil {
		if err := h.auditService.Log(c, &user.ID, &repo.ID, "branch.restore", gin.H{"branch": delBranch.BranchName}, c.ClientIP(), c.Request.UserAgent()); err != nil {
			log.Printf("WARNING: audit log failed for branch.restore: %v", err)
		}
	}

	response.SuccessWithMessage(c, "Branch restored successfully", nil)
}

// PermanentDeleteBranch handles DELETE /api/v1/repositories/:id/paperbin/branches/:branchId.
func (h *RepositoryHandler) PermanentDeleteBranch(c *gin.Context) {
	repoID, ok := parseUUIDParam(c, "id", "repository")
	if !ok {
		return
	}

	branchID, ok := parseUUIDParam(c, "branchId", "branch")
	if !ok {
		return
	}

	db := database.GetDB()

	var delBranch models.DeletedBranch
	if err := db.First(&delBranch, branchID).Error; err != nil {
		response.NotFound(c, "Deleted branch record not found")
		return
	}

	if delBranch.RepositoryID != repoID {
		response.BadRequest(c, "MISMATCH", "Branch does not belong to this repository")
		return
	}

	repo, err := h.repoService.GetRepositoryByID(c, repoID)
	if err == nil {
		paperbinRef := "refs/paperbin/heads/" + delBranch.BranchName
		_, _ = gitops.RunGitCommand(c, repo.StoragePath, "update-ref", "-d", paperbinRef)
	}

	if err := db.Delete(&delBranch).Error; err != nil {
		response.InternalError(c, "Failed to delete paperbin DB record: "+err.Error())
		return
	}

	user, _ := middleware.GetCurrentUser(c)
	if user != nil {
		if err := h.auditService.Log(c, &user.ID, &repoID, "branch.permanent_delete", gin.H{"branch": delBranch.BranchName}, c.ClientIP(), c.Request.UserAgent()); err != nil {
			log.Printf("WARNING: audit log failed for branch.permanent_delete: %v", err)
		}
	}

	response.SuccessWithMessage(c, "Pruned branch deleted permanently from paperbin", nil)
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		// Restrict authenticated log streaming to same-origin requests so other
		// websites cannot open a socket against a victim's session.
		origin := r.Header.Get("Origin")
		if origin == "" {
			// Non-browser clients (no Origin header) are allowed.
			return true
		}
		u, err := url.Parse(origin)
		if err != nil {
			return false
		}
		return strings.EqualFold(u.Host, r.Host)
	},
}

// StreamRepositoryLogs upgrades request to websocket and streams live progress logs.
func (h *RepositoryHandler) StreamRepositoryLogs(c *gin.Context) {
	repoID := c.Param("id")

	ws, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		log.Printf("WebSocket upgrade failed: %v", err)
		return
	}
	defer ws.Close()

	logChan := gitops.GlobalLogHub.Subscribe(repoID)
	defer gitops.GlobalLogHub.Unsubscribe(repoID, logChan)

	// A websocket has no read pump here, and a hijacked connection's request
	// context is not cancelled until this handler returns — so for an idle repo a
	// client that closed its tab would block the loop below forever, leaking this
	// goroutine and its LogHub subscription. Run a reader whose only job is to
	// notice the peer going away (ReadMessage erroring) and cancel.
	ctx, cancel := context.WithCancel(c.Request.Context())
	defer cancel()
	go func() {
		for {
			if _, _, rerr := ws.ReadMessage(); rerr != nil {
				cancel()
				return
			}
		}
	}()

	// Both the keep-alive goroutine and the log-streaming loop write to the same
	// websocket connection, so serialize all writes through a single mutex —
	// concurrent gorilla/websocket writes are not safe.
	var writeMu sync.Mutex
	writeMessage := func(messageType int, data []byte) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		return ws.WriteMessage(messageType, data)
	}

	// Keep alive ticker
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if err := writeMessage(websocket.PingMessage, []byte{}); err != nil {
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	for {
		select {
		case msg, ok := <-logChan:
			if !ok {
				return
			}
			if err := writeMessage(websocket.TextMessage, []byte(msg)); err != nil {
				return
			}
		case <-ctx.Done():
			return
		}
	}
}

// CodeSearchResult represents a single search match in a repository.
type CodeSearchResult struct {
	RepoID     string `json:"repo_id"`
	RepoName   string `json:"repo_name"`
	File       string `json:"file"`
	LineNumber string `json:"line_number"`
	Content    string `json:"content"`
}

// GlobalSearch handles GET /api/v1/search.
func (h *RepositoryHandler) GlobalSearch(c *gin.Context) {
	query := c.Query("q")
	if query == "" {
		response.BadRequest(c, "MISSING_QUERY", "Search query is required")
		return
	}

	// Reject overly broad queries before scanning every active repository.
	if len(strings.TrimSpace(query)) < 3 {
		response.BadRequest(c, "QUERY_TOO_SHORT", "Search query must be at least 3 characters")
		return
	}

	db := database.GetDB()
	var repos []models.Repository
	if err := db.Where("is_active = ?", true).Find(&repos).Error; err != nil {
		response.InternalError(c, err.Error())
		return
	}

	const (
		resultLimit    = 100              // cap results to bound payload size
		searchWorkers  = 8                // concurrent repos scanned at once
		perRepoTimeout = 10 * time.Second // one slow repo cannot stall the batch
		overallTimeout = 20 * time.Second // hard ceiling for the whole search
	)

	// Scan repositories concurrently (bounded) under one overall deadline instead
	// of sequentially with a per-repo timeout each — a large fleet previously made
	// worst-case latency the sum of every repo's timeout.
	overallCtx, cancelAll := context.WithTimeout(c.Request.Context(), overallTimeout)
	defer cancelAll()

	var (
		mu      sync.Mutex
		results []CodeSearchResult
		wg      sync.WaitGroup
		sem     = make(chan struct{}, searchWorkers)
	)

	for i := range repos {
		repo := repos[i]
		// Stop launching more work once we already have enough results.
		mu.Lock()
		done := len(results) >= resultLimit
		mu.Unlock()
		if done || overallCtx.Err() != nil {
			break
		}
		// Skip repos not present on disk without spending a worker slot.
		if _, err := os.Stat(repo.StoragePath); err != nil {
			continue
		}

		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()

			repoCtx, cancel := context.WithTimeout(overallCtx, perRepoTimeout)
			defer cancel()
			output, err := gitops.RunGitCommand(repoCtx, repo.StoragePath, "grep", "-nI", "--no-color", "-e", query, "HEAD")
			if err != nil {
				// git grep exits 1 when there are no matches, which is normal.
				return
			}

			for _, line := range strings.Split(output, "\n") {
				line = strings.TrimSpace(line)
				if line == "" {
					continue
				}
				// Format: HEAD:path/to/file:line_number:matching content line
				parts := strings.SplitN(line, ":", 4)
				if len(parts) < 4 {
					continue
				}
				mu.Lock()
				if len(results) >= resultLimit {
					mu.Unlock()
					cancelAll() // enough results — stop the remaining scans
					return
				}
				results = append(results, CodeSearchResult{
					RepoID:     repo.ID.String(),
					RepoName:   repo.Name,
					File:       strings.TrimPrefix(parts[0]+":"+parts[1], "HEAD:"),
					LineNumber: parts[2],
					Content:    parts[3],
				})
				mu.Unlock()
			}
		}()
	}

	wg.Wait()

	if len(results) > resultLimit {
		results = results[:resultLimit]
	}
	response.Success(c, results)
}
