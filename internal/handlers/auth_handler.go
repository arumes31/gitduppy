package handlers

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/gitduppy/gitduppy/internal/middleware"
	"github.com/gitduppy/gitduppy/internal/services"
	"github.com/gitduppy/gitduppy/pkg/response"
	"github.com/gitduppy/gitduppy/pkg/validator"
	"github.com/google/uuid"
)

// trustProxyHeaders controls whether forwarded headers (X-Forwarded-Proto) are
// honored. It is only safe to trust these behind a proxy that sets them, so it
// defaults to false and is enabled by SetTrustProxyHeaders when trusted proxies
// are configured. Otherwise a direct client could spoof the header.
//
//nolint:gochecknoglobals
var trustProxyHeaders bool

// SetTrustProxyHeaders enables honoring X-Forwarded-Proto. Call at startup when
// the server sits behind a trusted reverse proxy.
func SetTrustProxyHeaders(v bool) { trustProxyHeaders = v }

// requestIsHTTPS reports whether the request reached us over TLS, either
// directly, or via a terminating proxy that set X-Forwarded-Proto (only trusted
// when proxy headers are enabled).
func requestIsHTTPS(c *gin.Context) bool {
	if c.Request.TLS != nil {
		return true
	}
	return trustProxyHeaders && strings.EqualFold(c.GetHeader("X-Forwarded-Proto"), "https")
}

// setSessionCookie sets the session cookie with hardened flags: HttpOnly always,
// SameSite=Lax to blunt CSRF, and Secure whenever the request arrived over HTTPS.
func setSessionCookie(c *gin.Context, token string, maxAge int) {
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie("session", token, maxAge, "/", "", requestIsHTTPS(c), true)
}

// AuthHandler handles authentication requests.
type AuthHandler struct {
	authService  *services.AuthService
	auditService *services.AuditService
}

// NewAuthHandler creates a new auth handler.
func NewAuthHandler(authService *services.AuthService, auditService *services.AuditService) *AuthHandler {
	return &AuthHandler{
		authService:  authService,
		auditService: auditService,
	}
}

// audit records a security-relevant action, ignoring logging errors so an audit
// failure never blocks the primary request.
func (h *AuthHandler) audit(c *gin.Context, userID *uuid.UUID, action string, details map[string]interface{}) {
	if h.auditService == nil {
		return
	}
	_ = h.auditService.LogAction(c, userID, nil, action, details, c)
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
		h.audit(c, nil, "auth.login_failed", map[string]interface{}{"username": req.Username})
		response.Unauthorized(c, err.Error())
		return
	}

	// Set session cookie
	setSessionCookie(c, resp.SessionToken, 86400)

	if resp.User != nil {
		h.audit(c, &resp.User.ID, "auth.login", map[string]interface{}{"username": resp.User.Username})
	}

	response.SuccessWithMessage(c, "Login successful", gin.H{
		"user":          resp.User,
		"session_token": resp.SessionToken,
		"expires_at":    resp.ExpiresAt,
	})
}

// Logout handles POST /api/v1/auth/logout.
func (h *AuthHandler) Logout(c *gin.Context) {
	sessionToken, err := c.Cookie("session")
	var logoutErr error
	if err == nil && sessionToken != "" {
		logoutErr = h.authService.Logout(c, sessionToken)
	}

	// Always clear cookie
	setSessionCookie(c, "", -1)

	if logoutErr != nil {
		response.InternalError(c, "Failed to invalidate session: "+logoutErr.Error())
		return
	}

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
		h.audit(c, &user.ID, "auth.password_change_failed", nil)
		response.BadRequest(c, "INVALID_PASSWORD", err.Error())
		return
	}

	h.audit(c, &user.ID, "auth.password_changed", nil)
	response.SuccessWithMessage(c, "Password changed successfully", nil)
}
