package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/gitduppy/gitduppy/internal/config"
	"github.com/gitduppy/gitduppy/internal/middleware"
	"github.com/gitduppy/gitduppy/internal/services"
	"github.com/gitduppy/gitduppy/pkg/response"
)

// ConfigHandler handles configuration API requests.
type ConfigHandler struct {
	configService *services.ConfigService
}

// NewConfigHandler creates a new config handler.
func NewConfigHandler(configService *services.ConfigService) *ConfigHandler {
	return &ConfigHandler{
		configService: configService,
	}
}

// GetConfig handles GET /api/v1/config.
func (h *ConfigHandler) GetConfig(c *gin.Context) {
	user, ok := middleware.GetCurrentUser(c)
	if !ok || !user.IsAdmin() {
		response.Unauthorized(c, "Admin access required")
		return
	}

	cfg := h.configService.GetConfig(c)
	response.Success(c, cfg)
}

// UpdateConfig handles PUT /api/v1/config.
func (h *ConfigHandler) UpdateConfig(c *gin.Context) {
	user, ok := middleware.GetCurrentUser(c)
	if !ok || !user.IsAdmin() {
		response.Unauthorized(c, "Admin access required")
		return
	}

	var newConfig config.Config
	if err := c.ShouldBindJSON(&newConfig); err != nil {
		response.BadRequest(c, "INVALID_REQUEST", err.Error())
		return
	}

	if err := h.configService.UpdateConfig(c, &newConfig); err != nil {
		response.InternalError(c, "Failed to update configuration: "+err.Error())
		return
	}

	response.SuccessWithMessage(c, "Configuration updated successfully. Application restart required.", nil)
}

// UpdateOAuthSettingsRequest represents the payload to update OAuth settings.
type UpdateOAuthSettingsRequest struct {
	Provider     string `json:"provider" binding:"required,oneof=github gitlab google"`
	ClientID     string `json:"client_id" binding:"required"`
	ClientSecret string `json:"client_secret"`
}

// UpdateOAuthSettings handles PUT /api/v1/settings/oauth.
func (h *ConfigHandler) UpdateOAuthSettings(c *gin.Context) {
	user, ok := middleware.GetCurrentUser(c)
	if !ok || !user.IsAdmin() {
		response.Unauthorized(c, "Admin access required")
		return
	}

	var req UpdateOAuthSettingsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "INVALID_REQUEST", err.Error())
		return
	}

	idKey := "oauth2_" + req.Provider + "_client_id"
	secretKey := "oauth2_" + req.Provider + "_client_secret"

	// Fetch previous client_id for rollback/compensation
	var prevClientID string
	var hasPrevClient bool
	if prevVal, getErr := h.configService.GetSettingString(c, idKey); getErr == nil {
		prevClientID = prevVal
		hasPrevClient = true
	}

	// Save client_id
	if err := h.configService.SetSetting(c, idKey, req.ClientID, "OAuth Client ID for "+req.Provider, false); err != nil {
		response.ErrorResponse(c, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to save client_id")
		return
	}

	// Only update the secret if one was provided (don't overwrite with empty string unless intentional,
	// but here we just overwrite if provided. A robust implementation might handle partial updates).
	if req.ClientSecret != "" {
		if err := h.configService.SetSetting(c, secretKey, req.ClientSecret, "OAuth Client Secret for "+req.Provider, true); err != nil {
			// Rollback/compensate client_id write
			if hasPrevClient {
				_ = h.configService.SetSetting(c, idKey, prevClientID, "OAuth Client ID for "+req.Provider, false)
			} else {
				_ = h.configService.SetSetting(c, idKey, "", "OAuth Client ID for "+req.Provider, false)
			}
			response.ErrorResponse(c, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to save client_secret")
			return
		}
	}

	response.SuccessWithMessage(c, "OAuth configuration updated successfully.", nil)
}
