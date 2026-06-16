package handlers

import (
	"github.com/gin-gonic/gin"
	"github.com/gitduppy/gitduppy/internal/services"
	"github.com/gitduppy/gitduppy/pkg/response"
	"github.com/gitduppy/gitduppy/pkg/validator"
	"github.com/google/uuid"
)

// TagHandler handles tag requests
type TagHandler struct {
	tagService *services.TagService
}

// NewTagHandler creates a new tag handler
func NewTagHandler(tagService *services.TagService) *TagHandler {
	return &TagHandler{
		tagService: tagService,
	}
}

// ListTags handles GET /api/v1/tags
func (h *TagHandler) ListTags(c *gin.Context) {
	tags, err := h.tagService.ListTags(c)
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}

	response.Success(c, tags)
}

// GetTag handles GET /api/v1/tags/:id
func (h *TagHandler) GetTag(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "INVALID_ID", "Invalid tag ID format")
		return
	}

	tag, err := h.tagService.GetTagByID(c, id)
	if err != nil {
		response.NotFound(c, "Tag not found")
		return
	}

	response.Success(c, tag)
}

// CreateTag handles POST /api/v1/tags
func (h *TagHandler) CreateTag(c *gin.Context) {
	var req services.CreateTagRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "INVALID_REQUEST", err.Error())
		return
	}

	if err := validator.ValidateStruct(&req); err != nil {
		response.BadRequest(c, "VALIDATION_ERROR", err.Error())
		return
	}

	if !validator.ValidateColor(req.Color) {
		response.BadRequest(c, "INVALID_COLOR", "Color must be a valid hex color (e.g., #ff0000)")
		return
	}

	tag, err := h.tagService.CreateTag(c, &req)
	if err != nil {
		response.BadRequest(c, "CREATE_ERROR", err.Error())
		return
	}

	response.Created(c, tag)
}

// UpdateTag handles PUT /api/v1/tags/:id
func (h *TagHandler) UpdateTag(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "INVALID_ID", "Invalid tag ID format")
		return
	}

	var req services.UpdateTagRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "INVALID_REQUEST", err.Error())
		return
	}

	tag, err := h.tagService.UpdateTag(c, id, &req)
	if err != nil {
		response.BadRequest(c, "UPDATE_ERROR", err.Error())
		return
	}

	response.Success(c, tag)
}

// DeleteTag handles DELETE /api/v1/tags/:id
func (h *TagHandler) DeleteTag(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "INVALID_ID", "Invalid tag ID format")
		return
	}

	if err := h.tagService.DeleteTag(c, id); err != nil {
		response.BadRequest(c, "DELETE_ERROR", err.Error())
		return
	}

	response.SuccessWithMessage(c, "Tag deleted", nil)
}

// GetRepositoryTags handles GET /api/v1/repositories/:id/tags
func (h *TagHandler) GetRepositoryTags(c *gin.Context) {
	repoID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "INVALID_ID", "Invalid repository ID format")
		return
	}

	tags, err := h.tagService.GetRepositoryTags(c, repoID)
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}

	response.Success(c, tags)
}

// AddTagToRepository handles POST /api/v1/repositories/:id/tags
func (h *TagHandler) AddTagToRepository(c *gin.Context) {
	repoID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "INVALID_ID", "Invalid repository ID format")
		return
	}

	var req struct {
		TagID uuid.UUID `json:"tag_id" validate:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "INVALID_REQUEST", err.Error())
		return
	}

	if err := h.tagService.AddTagToRepository(c, repoID, req.TagID); err != nil {
		response.BadRequest(c, "ADD_ERROR", err.Error())
		return
	}

	response.SuccessWithMessage(c, "Tag added to repository", nil)
}

// RemoveTagFromRepository handles DELETE /api/v1/repositories/:id/tags/:tagId
func (h *TagHandler) RemoveTagFromRepository(c *gin.Context) {
	repoID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "INVALID_ID", "Invalid repository ID format")
		return
	}

	tagID, err := uuid.Parse(c.Param("tagId"))
	if err != nil {
		response.BadRequest(c, "INVALID_ID", "Invalid tag ID format")
		return
	}

	if err := h.tagService.RemoveTagFromRepository(c, repoID, tagID); err != nil {
		response.BadRequest(c, "REMOVE_ERROR", err.Error())
		return
	}

	response.SuccessWithMessage(c, "Tag removed from repository", nil)
}

// SetRepositoryTags handles PUT /api/v1/repositories/:id/tags
func (h *TagHandler) SetRepositoryTags(c *gin.Context) {
	repoID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "INVALID_ID", "Invalid repository ID format")
		return
	}

	var req struct {
		TagIDs []uuid.UUID `json:"tag_ids" validate:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "INVALID_REQUEST", err.Error())
		return
	}

	if err := h.tagService.SetRepositoryTags(c, repoID, req.TagIDs); err != nil {
		response.BadRequest(c, "UPDATE_ERROR", err.Error())
		return
	}

	response.SuccessWithMessage(c, "Repository tags updated", nil)
}
