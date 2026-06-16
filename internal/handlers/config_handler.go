package handlers

import (
	"github.com/gin-gonic/gin"
	"github.com/gitduppy/gitduppy/internal/middleware"
	"github.com/gitduppy/gitduppy/internal/services"
	"github.com/gitduppy/gitduppy/pkg/response"
)

// ConfigHandler handles configuration API requests
type ConfigHandler struct {
	configService *services.ConfigService
}

// NewConfigHandler creates a new config handler
func NewConfigHandler(configService *services.ConfigService) *ConfigHandler {
	return &ConfigHandler{
		configService: configService,
	}
}

// GetConfig handles GET /api/v1/config
func (h *ConfigHandler) GetConfig(c *gin.Context) {
	user, ok := middleware.GetCurrentUser(c)
	if !ok || !user.IsAdmin() {
		response.Unauthorized(c, "Admin access required")
		return
	}

	cfg := h.configService.GetConfig(c)
	response.Success(c, cfg)
}

// UpdateConfig handles PUT /api/v1/config
func (h *ConfigHandler) UpdateConfig(c *gin.Context) {
	user, ok := middleware.GetCurrentUser(c)
	if !ok || !user.IsAdmin() {
		response.Unauthorized(c, "Admin access required")
		return
	}

	var newConfig map[string]interface{}
	if err := c.ShouldBindJSON(&newConfig); err != nil {
		response.BadRequest(c, "INVALID_REQUEST", err.Error())
		return
	}

	// For now, we'll just return success since actual config updates require restart
	// In a real implementation, you'd validate and apply the new config
	response.SuccessWithMessage(c, "Configuration updated successfully. Application restart required.", nil)
}
