package middleware

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gitduppy/gitduppy/internal/models"
	"github.com/gitduppy/gitduppy/pkg/crypto"
	"github.com/gitduppy/gitduppy/pkg/response"
	"gorm.io/gorm"
)

// AuthMiddleware handles session and API key authentication.
type AuthMiddleware struct {
	// db is the injected database handle used by the validators. Injecting it
	// (rather than reaching for database.GetDB() on every request) keeps the
	// middleware testable and its dependency explicit.
	db *gorm.DB
	// cache memoizes validated users keyed by the at-rest credential hash so a
	// burst of authenticated requests from one client does not repeat the two-query
	// (credential lookup + user fetch) validation every time. See AuthCache for the
	// staleness/eviction contract.
	cache *AuthCache
	// Optional: exclude paths from authentication
	excludePaths []string
}

// NewAuthMiddleware creates a new auth middleware backed by the given database.
func NewAuthMiddleware(db *gorm.DB) *AuthMiddleware {
	return &AuthMiddleware{
		db:    db,
		cache: NewAuthCache(),
		excludePaths: []string{
			"/api/v1/health",
			"/api/v1/health/live",
			"/api/v1/health/ready",
			"/api/v1/auth/login",
			"/api/v1/webhooks/receive",
		},
	}
}

// Cache exposes the middleware's auth cache so the auth service can evict entries
// on logout / password change (see auth_service). It is safe for concurrent use.
func (m *AuthMiddleware) Cache() *AuthCache { return m.cache }

// Stop releases the middleware's background resources (the auth cache sweeper).
func (m *AuthMiddleware) Stop() {
	if m.cache != nil {
		m.cache.Stop()
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
			user, err := m.validateAPIKey(c.Request.Context(), apiKey)
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
			user, err := m.validateSession(c.Request.Context(), sessionCookie)
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
			user, err := m.validateSession(c.Request.Context(), sessionCookie)
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
func (m *AuthMiddleware) validateAPIKey(ctx context.Context, key string) (*models.User, error) {
	// API keys are stored as the SHA-256 of the raw key; hash before lookup.
	keyHash := crypto.HashToken(key)

	// Fast path: a recently validated key is served from cache, skipping both DB
	// queries. Bounded by authCacheTTL (a revoked/expired key is honored at most
	// that long); see AuthCache.
	if m.cache != nil {
		if user, ok := m.cache.get(keyHash); ok {
			return user, nil
		}
	}

	db := m.db
	if db == nil {
		return nil, http.ErrNoCookie
	}

	// Find the API key in database
	var apiKey models.APIKey
	if err := db.WithContext(ctx).Where("key_hash = ? AND is_active = true", keyHash).First(&apiKey).Error; err != nil {
		return nil, err
	}

	// Check if expired
	if apiKey.ExpiresAt != nil && apiKey.ExpiresAt.Before(time.Now()) {
		return nil, http.ErrNoCookie
	}

	// Throttle last_used_at writes to at most once per 5 minutes with a single
	// conditional UPDATE (no extra read, race-free). A no-op update is silent and,
	// like any failure here, best-effort — it must never fail authentication.
	now := time.Now().UTC()
	db.WithContext(ctx).Model(&models.APIKey{}).
		Where("id = ? AND (last_used_at IS NULL OR last_used_at < ?)", apiKey.ID, now.Add(-5*time.Minute)).
		Update("last_used_at", now)

	// Get the user
	var user models.User
	if err := db.WithContext(ctx).First(&user, apiKey.UserID).Error; err != nil {
		return nil, err
	}

	// Check if user is active
	if !user.IsActive {
		return nil, http.ErrNoCookie
	}

	if m.cache != nil {
		m.cache.set(keyHash, &user)
	}
	return &user, nil
}

// validateSession validates a session token and returns the associated user.
func (m *AuthMiddleware) validateSession(ctx context.Context, token string) (*models.User, error) {
	// Sessions are keyed by the SHA-256 of the token, so hash the raw cookie value
	// before looking it up; the raw token never touches the database.
	tokenHash := crypto.HashToken(token)

	// Fast path: a recently validated session is served from cache, skipping the
	// session lookup and user fetch. Bounded by authCacheTTL (a logged-out or
	// deactivated user is served at most that long unless evicted); see AuthCache.
	if m.cache != nil {
		if user, ok := m.cache.get(tokenHash); ok {
			return user, nil
		}
	}

	db := m.db
	if db == nil {
		return nil, http.ErrNoCookie
	}

	// Find the session in database
	var session models.Session
	if err := db.WithContext(ctx).Where("token = ? AND expiry > ?", tokenHash, time.Now().UTC()).First(&session).Error; err != nil {
		return nil, err
	}

	// Get the user
	var user models.User
	if err := db.WithContext(ctx).First(&user, session.UserID).Error; err != nil {
		return nil, err
	}

	// Check if user is active
	if !user.IsActive {
		return nil, http.ErrNoCookie
	}

	if m.cache != nil {
		m.cache.set(tokenHash, &user)
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
