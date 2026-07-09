package middleware

import (
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/gitduppy/gitduppy/internal/config"
)

// isStaticAsset reports whether a path serves immutable, cacheable static assets
// (JS/CSS/fonts/images) rather than dynamic API/HTML responses.
func isStaticAsset(path string) bool {
	return strings.HasPrefix(path, "/assets/") || strings.HasPrefix(path, "/static/")
}

// SecurityHeaders returns a middleware that adds security headers with config.
func SecurityHeaders(cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Static assets are safe (and desirable) to cache; everything else is
		// dynamic and must not be stored. Set the caching headers up front so the
		// asset branch can opt out of the no-store defaults below.
		if isStaticAsset(c.Request.URL.Path) {
			c.Header("X-Content-Type-Options", "nosniff")
			// These protections must also cover static responses (e.g. an
			// inline-rendered SVG): pin HTTPS, forbid framing, and limit the
			// referer. Only the caching policy differs from dynamic responses.
			c.Header("Strict-Transport-Security", "max-age="+strconv.Itoa(cfg.Security.HSTSMaxAge)+"; includeSubDomains; preload")
			c.Header("X-Frame-Options", "DENY")
			c.Header("Referrer-Policy", "strict-origin-when-cross-origin")
			c.Header("Cache-Control", "public, max-age=86400")
			c.Next()
			return
		}

		// Prevent MIME type sniffing
		c.Header("X-Content-Type-Options", "nosniff")

		// Prevent clickjacking
		c.Header("X-Frame-Options", "DENY")

		// XSS protection
		c.Header("X-XSS-Protection", "1; mode=block")

		// HSTS (only in production with HTTPS) - made max-age configurable
		c.Header("Strict-Transport-Security", "max-age="+strconv.Itoa(cfg.Security.HSTSMaxAge)+"; includeSubDomains; preload")

		// Content Security Policy. 'unsafe-inline' in script-src/style-src is
		// required for now because the templates still use inline onclick handlers
		// and style attributes; removing those inline hooks is explicitly out of
		// scope, so the directive stays until they are migrated.
		//
		// connect-src 'self' covers the same-origin WebSocket log stream
		// (ws://<host>/api/v1/repositories/:id/logs/stream): under CSP Level 3,
		// 'self' matches same-origin ws:/wss: in modern browsers, so no explicit
		// ws:/wss: source is needed. frame-ancestors 'none' hardens against
		// clickjacking alongside X-Frame-Options.
		c.Header("Content-Security-Policy", "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'")

		// Referrer Policy
		c.Header("Referrer-Policy", "strict-origin-when-cross-origin")

		// Permissions Policy - added more restrictions
		c.Header("Permissions-Policy", "accelerometer=(), ambient-light-sensor=(), autoplay=(), battery=(), camera=(), display-capture=(), document-domain=(), encrypted-media=(), execution-while-not-rendered=(), execution-while-out-of-viewport=(), fullscreen=(), geolocation=(), gyroscope=(), magnetometer=(), microphone=(), midi=(), navigation-override=(), payment=(), picture-in-picture=(), publickey-credentials-get=(), screen-wake-lock=(), sync-xhr=(), usb=(), web-share=(), xr-spatial-tracking=()")

		// Cache control for API responses
		c.Header("Cache-Control", "no-store, no-cache, must-revalidate, proxy-revalidate")
		c.Header("Pragma", "no-cache")
		c.Header("Expires", "0")

		c.Next()
	}
}

// NoCache returns a middleware that disables caching.
func NoCache() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Cache-Control", "no-store, no-cache, must-revalidate, proxy-revalidate")
		c.Header("Pragma", "no-cache")
		c.Header("Expires", "0")
		c.Next()
	}
}
