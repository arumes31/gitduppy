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
			// HSTS must be sent on every response from the origin, including
			// cacheable static assets, so browsers pin HTTPS regardless of
			// which path first loads.
			c.Header("Strict-Transport-Security", "max-age="+strconv.Itoa(cfg.Security.HSTSMaxAge)+"; includeSubDomains; preload")
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

		// Content Security Policy
		c.Header("Content-Security-Policy", "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data: https:; font-src 'self' data:;")

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
