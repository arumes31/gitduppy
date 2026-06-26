package middleware

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gitduppy/gitduppy/internal/database"
	"github.com/gitduppy/gitduppy/internal/models"
	"github.com/gitduppy/gitduppy/pkg/response"
)

// AuthMiddleware handles session and API key authentication.
type AuthMiddleware struct {
	// Optional: exclude paths from authentication
	excludePaths []string
}

// NewAuthMiddleware creates a new auth middleware.
func NewAuthMiddleware() *AuthMiddleware {
	return &AuthMiddleware{
		excludePaths: []string{
			"/api/v1/health",
			"/api/v1/health/live",
			"/api/v1/health/ready",
			"/api/v1/auth/login",
			"/api/v1/webhooks/receive",
		},
	}
}

// Middleware returns the authentication middleware function.
func (m *AuthMiddleware) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Check if path is excluded
		if m.isExcluded(c.Request.URL.Path) {
			c.Next()
			return
		}

		// Try API key authentication first (from Authorization header)
		authHeader := c.GetHeader("Authorization")
		if strings.HasPrefix(authHeader, "Bearer ") {
			apiKey := strings.TrimPrefix(authHeader, "Bearer ")
			user, err := m.validateAPIKey(apiKey)
			if err == nil {
				c.Set("user", user)
				c.Set("auth_type", "api_key")
				c.Next()
				return
			}
		}

		// Try session authentication (from cookie)
		sessionCookie, err := c.Cookie("session")
		if err == nil && sessionCookie != "" {
			user, err := m.validateSession(sessionCookie)
			if err == nil {
				c.Set("user", user)
				c.Set("auth_type", "session")
				c.Next()
				return
			}
		}

		// No valid authentication found
		response.Unauthorized(c, "Invalid or missing authentication credentials")
		c.Abort()
	}
}

// WebMiddleware returns the authentication middleware function for web UI that redirects to login instead of returning JSON.
func (m *AuthMiddleware) WebMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Try session authentication (from cookie)
		sessionCookie, err := c.Cookie("session")
		if err == nil && sessionCookie != "" {
			user, err := m.validateSession(sessionCookie)
			if err == nil {
				c.Set("user", user)
				c.Set("auth_type", "session")
				c.Next()
				return
			}
		}

		// No valid authentication found, redirect to login
		c.Redirect(http.StatusFound, "/login")
		c.Abort()
	}
}

// isExcluded checks if a path should be excluded from authentication.
func (m *AuthMiddleware) isExcluded(path string) bool {
	for _, excluded := range m.excludePaths {
		if strings.HasPrefix(path, excluded) {
			return true
		}
	}
	return false
}

// validateAPIKey validates an API key and returns the associated user.
func (m *AuthMiddleware) validateAPIKey(key string) (*models.User, error) {
	db := database.GetDB()
	if db == nil {
		return nil, http.ErrNoCookie
	}

	// Hash the provided key
	hash := sha256.Sum256([]byte(key))
	keyHash := hex.EncodeToString(hash[:])

	// Find the API key in database
	var apiKey models.APIKey
	if err := db.Where("key_hash = ? AND is_active = true", keyHash).First(&apiKey).Error; err != nil {
		return nil, err
	}

	// Check if expired
	if apiKey.ExpiresAt != nil && apiKey.ExpiresAt.Before(time.Now()) {
		return nil, http.ErrNoCookie
	}

	// Update last used
	db.Model(&apiKey).Update("last_used_at", time.Now())

	// Get the user
	var user models.User
	if err := db.First(&user, apiKey.UserID).Error; err != nil {
		return nil, err
	}

	// Check if user is active
	if !user.IsActive {
		return nil, http.ErrNoCookie
	}

	return &user, nil
}

// validateSession validates a session token and returns the associated user.
func (m *AuthMiddleware) validateSession(token string) (*models.User, error) {
	db := database.GetDB()
	if db == nil {
		return nil, http.ErrNoCookie
	}

	// Find the session in database
	var session models.Session
	if err := db.Where("token = ? AND expiry > ?", token, time.Now()).First(&session).Error; err != nil {
		return nil, err
	}

	// Get the user
	var user models.User
	if err := db.First(&user, session.UserID).Error; err != nil {
		return nil, err
	}

	// Check if user is active
	if !user.IsActive {
		return nil, http.ErrNoCookie
	}

	return &user, nil
}

// RequireAdmin returns a middleware that requires admin role.
func RequireAdmin() gin.HandlerFunc {
	return func(c *gin.Context) {
		user, exists := c.Get("user")
		if !exists {
			response.Unauthorized(c, "Authentication required")
			c.Abort()
			return
		}

		u, ok := user.(*models.User)
		if !ok || !u.IsAdmin() {
			response.Forbidden(c, "Admin access required")
			c.Abort()
			return
		}

		c.Next()
	}
}

// GetCurrentUser returns the current authenticated user from context.
func GetCurrentUser(c *gin.Context) (*models.User, bool) {
	user, exists := c.Get("user")
	if !exists {
		return nil, false
	}
	u, ok := user.(*models.User)
	return u, ok
}
