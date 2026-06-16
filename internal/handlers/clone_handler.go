package handlers

import (
	"github.com/gin-gonic/gin"
	"github.com/gitduppy/gitduppy/internal/services"
	"github.com/gitduppy/gitduppy/pkg/response"
	"github.com/gitduppy/gitduppy/pkg/validator"
	"github.com/google/uuid"
)

// CloneHandler handles clone job requests
type CloneHandler struct {
	cloneService *services.CloneService
}

// NewCloneHandler creates a new clone handler
func NewCloneHandler(cloneService *services.CloneService) *CloneHandler {
	return &CloneHandler{
		cloneService: cloneService,
	}
}

// ListCloneJobs handles GET /api/v1/clone-jobs
func (h *CloneHandler) ListCloneJobs(c *gin.Context) {
	filter := &services.CloneFilter{
		Page:    1,
		PerPage: 20,
	}

	if page := c.Query("page"); page != "" {
		filter.Page = validator.ParseInt(page, 1)
	}
	if perPage := c.Query("per_page"); perPage != "" {
		filter.PerPage = validator.ParseInt(perPage, 20)
	}

	repoID := c.Query("repository_id")
	if repoID != "" {
		id, err := uuid.Parse(repoID)
		if err == nil {
			filter.RepositoryID = &id
		}
	}

	status := c.Query("status")
	if status != "" {
		filter.Status = &status
	}

	triggerType := c.Query("trigger_type")
	if triggerType != "" {
		filter.TriggerType = &triggerType
	}

	jobs, total, err := h.cloneService.ListCloneJobs(c, filter)
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}

	response.SuccessWithMeta(c, jobs, &response.Meta{
		Page:       filter.Page,
		PerPage:    filter.PerPage,
		Total:      int(total),
		TotalPages: int(total/int64(filter.PerPage)) + 1,
	})
}

// GetCloneJob handles GET /api/v1/clone-jobs/:id
func (h *CloneHandler) GetCloneJob(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "INVALID_ID", "Invalid clone job ID format")
		return
	}

	job, err := h.cloneService.GetCloneJobByID(c, id)
	if err != nil {
		response.NotFound(c, "Clone job not found")
		return
	}

	response.Success(c, job)
}

// CancelCloneJob handles POST /api/v1/clone-jobs/:id/cancel
func (h *CloneHandler) CancelCloneJob(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "INVALID_ID", "Invalid clone job ID format")
		return
	}

	if err := h.cloneService.CancelCloneJob(c, id); err != nil {
		response.BadRequest(c, "CANCEL_ERROR", err.Error())
		return
	}

	response.SuccessWithMessage(c, "Clone job cancelled", nil)
}

// ListRepositoryJobs handles GET /api/v1/repositories/:id/jobs
func (h *CloneHandler) ListRepositoryJobs(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "INVALID_ID", "Invalid repository ID format")
		return
	}

	filter := &services.CloneFilter{
		RepositoryID: &id,
		Page:         1,
		PerPage:      20,
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

	jobs, total, err := h.cloneService.ListCloneJobs(c, filter)
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}

	response.SuccessWithMeta(c, jobs, &response.Meta{
		Page:       filter.Page,
		PerPage:    filter.PerPage,
		Total:      int(total),
		TotalPages: int(total/int64(filter.PerPage)) + 1,
	})
}

// GetCloneJobLogs handles GET /api/v1/clone-jobs/:id/logs
func (h *CloneHandler) GetCloneJobLogs(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "INVALID_ID", "Invalid clone job ID format")
		return
	}

	logs, err := h.cloneService.GetCloneLogs(c, id)
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}

	response.Success(c, logs)
}
