package handlers

import (
	"github.com/gin-gonic/gin"
	"github.com/gitduppy/gitduppy/internal/middleware"
	"github.com/gitduppy/gitduppy/internal/services"
	"github.com/gitduppy/gitduppy/pkg/response"
	"github.com/gitduppy/gitduppy/pkg/validator"
)

// AuthHandler handles authentication requests.
type AuthHandler struct {
	authService *services.AuthService
}

// NewAuthHandler creates a new auth handler.
func NewAuthHandler(authService *services.AuthService) *AuthHandler {
	return &AuthHandler{
		authService: authService,
	}
}

// LoginRequest represents a login request.
type LoginRequest struct {
	Username   string `json:"username" validate:"required"`
	Password   string `json:"password" validate:"required"`
	RememberMe bool   `json:"remember_me"`
}

// Login handles POST /api/v1/auth/login.
func (h *AuthHandler) Login(c *gin.Context) {
	var req LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "INVALID_REQUEST", err.Error())
		return
	}

	if err := validator.ValidateStruct(&req); err != nil {
		response.BadRequest(c, "VALIDATION_ERROR", err.Error())
		return
	}

	resp, err := h.authService.Login(c, &services.LoginRequest{
		Username:   req.Username,
		Password:   req.Password,
		RememberMe: req.RememberMe,
	})
	if err != nil {
		response.Unauthorized(c, err.Error())
		return
	}

	// Set session cookie
	c.SetCookie("session", resp.SessionToken, 86400, "/", "", false, true)

	response.SuccessWithMessage(c, "Login successful", gin.H{
		"user":          resp.User,
		"session_token": resp.SessionToken,
		"expires_at":    resp.ExpiresAt,
	})
}

// Logout handles POST /api/v1/auth/logout.
func (h *AuthHandler) Logout(c *gin.Context) {
	sessionToken, err := c.Cookie("session")
	if err == nil && sessionToken != "" {
		if logoutErr := h.authService.Logout(c, sessionToken); logoutErr != nil {
			response.InternalError(c, "Failed to invalidate session: "+logoutErr.Error())
			return
		}
	}

	// Clear cookie
	c.SetCookie("session", "", -1, "/", "", false, true)

	response.SuccessWithMessage(c, "Logout successful", nil)
}

// Me handles GET /api/v1/auth/me.
func (h *AuthHandler) Me(c *gin.Context) {
	user, ok := middleware.GetCurrentUser(c)
	if !ok {
		response.Unauthorized(c, "Not authenticated")
		return
	}

	response.Success(c, gin.H{
		"id":         user.ID,
		"username":   user.Username,
		"email":      user.Email,
		"role":       user.Role,
		"is_active":  user.IsActive,
		"last_login": user.LastLogin,
	})
}

// Refresh handles POST /api/v1/auth/refresh.
func (h *AuthHandler) Refresh(c *gin.Context) {
	sessionToken, err := c.Cookie("session")
	if err != nil || sessionToken == "" {
		response.Unauthorized(c, "No session found")
		return
	}

	session, err := h.authService.RefreshSession(c, sessionToken)
	if err != nil {
		response.Unauthorized(c, err.Error())
		return
	}

	response.Success(c, gin.H{
		"session_token": session.Token,
		"expires_at":    session.Expiry,
	})
}

// ChangePassword handles POST /api/v1/auth/change-password.
func (h *AuthHandler) ChangePassword(c *gin.Context) {
	user, ok := middleware.GetCurrentUser(c)
	if !ok {
		response.Unauthorized(c, "Not authenticated")
		return
	}

	var req struct {
		OldPassword string `json:"old_password" validate:"required"`
		NewPassword string `json:"new_password" validate:"required,min=8"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "INVALID_REQUEST", err.Error())
		return
	}

	if err := validator.ValidateStruct(&req); err != nil {
		response.BadRequest(c, "VALIDATION_ERROR", err.Error())
		return
	}

	if err := h.authService.ChangePassword(c, user.ID, req.OldPassword, req.NewPassword); err != nil {
		response.BadRequest(c, "INVALID_PASSWORD", err.Error())
		return
	}

	response.SuccessWithMessage(c, "Password changed successfully", nil)
}
