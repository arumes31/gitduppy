package handlers

import (
	"github.com/gin-gonic/gin"
	"github.com/gitduppy/gitduppy/internal/middleware"
	"github.com/gitduppy/gitduppy/internal/services"
	"github.com/gitduppy/gitduppy/pkg/response"
	"github.com/gitduppy/gitduppy/pkg/validator"
	"github.com/google/uuid"
)

// APIKeyHandler handles API key requests
type APIKeyHandler struct {
	apiKeyService *services.APIKeyService
}

// NewAPIKeyHandler creates a new API key handler
func NewAPIKeyHandler(apiKeyService *services.APIKeyService) *APIKeyHandler {
	return &APIKeyHandler{
		apiKeyService: apiKeyService,
	}
}

// ListAPIKeys handles GET /api/v1/api-keys
func (h *APIKeyHandler) ListAPIKeys(c *gin.Context) {
	user, ok := middleware.GetCurrentUser(c)
	if !ok {
		response.Unauthorized(c, "Not authenticated")
		return
	}

	keys, err := h.apiKeyService.ListAPIKeys(c, user.ID)
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}

	response.Success(c, keys)
}

// CreateAPIKey handles POST /api/v1/api-keys
func (h *APIKeyHandler) CreateAPIKey(c *gin.Context) {
	user, ok := middleware.GetCurrentUser(c)
	if !ok {
		response.Unauthorized(c, "Not authenticated")
		return
	}

	var req services.CreateAPIKeyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "INVALID_REQUEST", err.Error())
		return
	}

	if err := validator.ValidateStruct(&req); err != nil {
		response.BadRequest(c, "VALIDATION_ERROR", err.Error())
		return
	}

	resp, err := h.apiKeyService.CreateAPIKey(c, user.ID, &req)
	if err != nil {
		response.BadRequest(c, "CREATE_ERROR", err.Error())
		return
	}

	response.Created(c, gin.H{
		"id":         resp.ID,
		"name":       resp.Name,
		"key":        resp.Key,
		"key_prefix": resp.KeyPrefix,
		"expires_at": resp.ExpiresAt,
		"created_at": resp.CreatedAt,
	})
}

// DeleteAPIKey handles DELETE /api/v1/api-keys/:id
func (h *APIKeyHandler) DeleteAPIKey(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "INVALID_ID", "Invalid API key ID format")
		return
	}

	if err := h.apiKeyService.DeleteAPIKey(c, id); err != nil {
		response.BadRequest(c, "DELETE_ERROR", err.Error())
		return
	}

	response.SuccessWithMessage(c, "API key revoked", nil)
}

// RevokeAPIKey handles POST /api/v1/api-keys/:id/revoke
func (h *APIKeyHandler) RevokeAPIKey(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "INVALID_ID", "Invalid API key ID format")
		return
	}

	if err := h.apiKeyService.RevokeAPIKey(c, id); err != nil {
		response.BadRequest(c, "REVOKE_ERROR", err.Error())
		return
	}

	response.SuccessWithMessage(c, "API key revoked", nil)
}
