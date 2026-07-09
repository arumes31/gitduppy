package handlers

import (
	"net/http"
	"strings"
	"time"

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

// cookieSecure forces the Secure flag on the session cookie regardless of how
// the current request arrived. It is driven by config (security.cookie_secure)
// and set once at startup via SetCookieSecure.
//
//nolint:gochecknoglobals
var cookieSecure bool

// SetCookieSecure sets whether the session cookie is always marked Secure. The
// value comes from security.cookie_secure (which defaults to the base_url
// scheme). Call at startup.
func SetCookieSecure(v bool) { cookieSecure = v }

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
// SameSite=Lax to blunt CSRF, Path=/, and Secure driven by config
// (security.cookie_secure). The per-request HTTPS check is OR'd in as a floor so
// a cookie set over a genuine TLS connection is always Secure even if the config
// value is left off — the flag can only ever be added, never removed. The same
// helper is used for login, the OAuth callback, and the logout deletion cookie
// so all three carry identical flags.
func setSessionCookie(c *gin.Context, token string, maxAge int) {
	c.SetSameSite(http.SameSiteLaxMode)
	secure := cookieSecure || requestIsHTTPS(c)
	c.SetCookie("session", token, maxAge, "/", "", secure, true)
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
func (h *AuthHandler) audit(c *gin.Context, userID *uuid.UUID, action string, details map[string]any) {
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
		h.audit(c, nil, "auth.login_failed", map[string]any{"username": req.Username})
		response.Unauthorized(c, err.Error())
		return
	}

	// Set the cookie lifetime to match the server-side session expiry (which
	// honors RememberMe), clamped to zero so a past expiry becomes a session
	// cookie rather than a negative max-age.
	maxAge := int(time.Until(resp.ExpiresAt).Seconds())
	if maxAge < 0 {
		maxAge = 0
	}
	setSessionCookie(c, resp.SessionToken, maxAge)

	if resp.User != nil {
		h.audit(c, &resp.User.ID, "auth.login", map[string]any{"username": resp.User.Username})
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
		logServerError(c, logoutErr)
		response.InternalError(c, "Failed to invalidate session")
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

	// Return the caller's own raw token (from the cookie), never session.Token —
	// which now holds the at-rest SHA-256 hash, not a usable token.
	response.Success(c, gin.H{
		"session_token": sessionToken,
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
