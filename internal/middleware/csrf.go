package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/csrf"
)

// CSRFMiddleware provides CSRF protection using gorilla/csrf.
type CSRFMiddleware struct {
	csrfKey    []byte
	secure     bool
	cookieName string
	headerName string
}

// NewCSRFMiddleware creates a new CSRF middleware instance.
func NewCSRFMiddleware(csrfKey string, secure bool) *CSRFMiddleware {
	return &CSRFMiddleware{
		csrfKey:    []byte(csrfKey),
		secure:     secure,
		cookieName: "gorilla.csrf.Token",
		headerName: "X-CSRF-Token",
	}
}

// Middleware returns a gin.HandlerFunc that provides CSRF protection.
func (m *CSRFMiddleware) Middleware() gin.HandlerFunc {
	errorHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("CSRF token invalid"))
	})

	// csrf.Protect returns func(http.Handler) http.Handler
	protect := csrf.Protect(
		m.csrfKey,
		csrf.Secure(m.secure),
		csrf.CookieName(m.cookieName),
		csrf.RequestHeader(m.headerName),
		csrf.FieldName("csrf_token"),
		csrf.ErrorHandler(errorHandler),
	)

	return func(c *gin.Context) {
		// Skip CSRF check for GET, HEAD, OPTIONS requests
		if c.Request.Method == http.MethodGet ||
			c.Request.Method == http.MethodHead ||
			c.Request.Method == http.MethodOptions {

			c.Next()
			return
		}

		// Wrap the gin handler chain with CSRF protection.
		var csrfPassed bool
		nextHandler := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			csrfPassed = true
			c.Request = r
			c.Next()
		})

		protect(nextHandler).ServeHTTP(c.Writer, c.Request)

		if !csrfPassed {
			c.Abort()
		}
	}
}

// GetCSRFToken extracts the CSRF token from the request context.
func GetCSRFToken(c *gin.Context) string {
	return csrf.Token(c.Request)
}
