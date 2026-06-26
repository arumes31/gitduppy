package handlers

import (
	"log"

	"github.com/gin-gonic/gin"
	"github.com/gitduppy/gitduppy/internal/middleware"
	"github.com/gitduppy/gitduppy/internal/services"
	"github.com/gitduppy/gitduppy/pkg/crypto"
	"github.com/gitduppy/gitduppy/pkg/response"
	"github.com/gitduppy/gitduppy/pkg/validator"
	"github.com/google/uuid"
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
		TotalPages: int(total/int64(filter.PerPage)) + 1,
	})
}

// GetRepository handles GET /api/v1/repositories/:id.
func (h *RepositoryHandler) GetRepository(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "INVALID_ID", "Invalid repository ID format")
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
		StoragePath          string                     `json:"storage_path" validate:"required"`
		IsBare               bool                       `json:"is_bare"`
		LFSEnabled           bool                       `json:"lfs_enabled"`
		MirrorIssues         bool                       `json:"mirror_issues"`
		MirrorPullRequests   bool                       `json:"mirror_pull_requests"`
		MirrorReleases       bool                       `json:"mirror_releases"`
		MirrorWiki           bool                       `json:"mirror_wiki"`
		CloneIntervalMinutes int                        `json:"clone_interval_minutes" validate:"min=5"`
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
		StoragePath:          req.StoragePath,
		IsBare:               req.IsBare,
		LFSEnabled:           req.LFSEnabled,
		MirrorIssues:         req.MirrorIssues,
		MirrorPullRequests:   req.MirrorPullRequests,
		MirrorReleases:       req.MirrorReleases,
		MirrorWiki:           req.MirrorWiki,
		CloneIntervalMinutes: req.CloneIntervalMinutes,
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
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "INVALID_ID", "Invalid repository ID format")
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
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "INVALID_ID", "Invalid repository ID format")
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
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "INVALID_ID", "Invalid repository ID format")
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
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "INVALID_ID", "Invalid repository ID format")
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
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "INVALID_ID", "Invalid repository ID format")
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
