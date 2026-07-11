package middleware

import "github.com/gin-gonic/gin"

// StaticCacheControl sets a revalidation-friendly Cache-Control on static asset
// responses. The assets are served by plain path with no content hash in the URL
// (there is no build step), so a long/immutable max-age is unsafe — it would pin
// stale CSS/JS in browsers across a deploy. Instead assets may be reused for an
// hour and must be revalidated afterwards; gin's underlying http.FileServer emits
// Last-Modified and answers If-Modified-Since with a 304, so that revalidation is
// a cheap conditional request rather than a full re-download. Cache busting on
// deploy is handled separately by appending ?v=<version> to the app.js/CSS refs in
// the base template.
func StaticCacheControl() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Cache-Control", "public, max-age=3600, must-revalidate")
		c.Next()
	}
}
