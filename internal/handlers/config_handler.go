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

const maintenanceModeKey = "maintenance_mode"

// GetMaintenanceMode handles GET /api/v1/config/maintenance.
// Available to any authenticated user so the UI can render a banner.
func (h *ConfigHandler) GetMaintenanceMode(c *gin.Context) {
	enabled := false
	if val, err := h.configService.GetSettingString(c, maintenanceModeKey); err == nil {
		enabled = val == "true"
	}
	response.Success(c, gin.H{"enabled": enabled})
}

// UpdateMaintenanceModeRequest represents the payload to toggle maintenance mode.
type UpdateMaintenanceModeRequest struct {
	Enabled *bool `json:"enabled" binding:"required"`
}

// UpdateMaintenanceMode handles PUT /api/v1/config/maintenance (admin only).
func (h *ConfigHandler) UpdateMaintenanceMode(c *gin.Context) {
	user, ok := middleware.GetCurrentUser(c)
	if !ok || !user.IsAdmin() {
		response.Unauthorized(c, "Admin access required")
		return
	}

	var req UpdateMaintenanceModeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "INVALID_REQUEST", err.Error())
		return
	}

	value := "false"
	if *req.Enabled {
		value = "true"
	}

	if err := h.configService.SetSetting(c, maintenanceModeKey, value, "Pause scheduled mirroring while maintenance is in progress", false); err != nil {
		response.InternalError(c, "Failed to update maintenance mode")
		return
	}

	response.SuccessWithMessage(c, "Maintenance mode updated.", gin.H{"enabled": *req.Enabled})
}

// GetQuota handles GET /api/v1/config/quota.
func (h *ConfigHandler) GetQuota(c *gin.Context) {
	val, err := h.configService.GetSettingString(c, "paperbin_quota_gb")
	if err != nil || val == "" {
		val = "50"
	}
	response.Success(c, gin.H{"quota_gb": val})
}

// UpdateQuota handles PUT /api/v1/config/quota (admin only).
func (h *ConfigHandler) UpdateQuota(c *gin.Context) {
	user, ok := middleware.GetCurrentUser(c)
	if !ok || !user.IsAdmin() {
		response.Unauthorized(c, "Admin access required")
		return
	}

	var req struct {
		QuotaGB string `json:"quota_gb" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "INVALID_REQUEST", err.Error())
		return
	}

	if err := h.configService.SetSetting(c, "paperbin_quota_gb", req.QuotaGB, "Paperbin Storage Quota in GB", false); err != nil {
		response.InternalError(c, err.Error())
		return
	}

	response.SuccessWithMessage(c, "Quota updated successfully", nil)
}
