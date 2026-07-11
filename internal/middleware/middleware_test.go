package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/gitduppy/gitduppy/internal/config"
	"github.com/gitduppy/gitduppy/internal/models"
)

func init() { gin.SetMode(gin.TestMode) }

func TestCORSWildcardDoesNotReflectWithCredentials(t *testing.T) {
	// With the wildcard default config, an arbitrary origin must NOT be echoed
	// back together with credentials (that would expose authenticated responses
	// to any site). It gets "*" and no credentials instead.
	r := gin.New()
	r.Use(CORS(DefaultCORSConfig()))
	r.GET("/", func(c *gin.Context) { c.Status(200) })

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Origin", "https://app.example")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("allow-origin = %q, want *", got)
	}
	if got := w.Header().Get("Access-Control-Allow-Credentials"); got != "" {
		t.Errorf("allow-credentials = %q, want empty for wildcard match", got)
	}
}

func TestCORSExplicitOriginGetsCredentials(t *testing.T) {
	// An explicitly allow-listed origin may be echoed back with credentials.
	cfg := &CORSConfig{
		AllowOrigins:     []string{"https://app.example"},
		AllowMethods:     []string{"GET"},
		AllowHeaders:     []string{"Content-Type"},
		AllowCredentials: true,
		MaxAge:           600,
	}
	r := gin.New()
	r.Use(CORS(cfg))
	r.GET("/", func(c *gin.Context) { c.Status(200) })

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Origin", "https://app.example")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "https://app.example" {
		t.Errorf("allow-origin = %q", got)
	}
	if w.Header().Get("Access-Control-Allow-Credentials") != "true" {
		t.Error("missing allow-credentials for explicit origin")
	}
}

func TestCORSPreflight(t *testing.T) {
	r := gin.New()
	r.Use(CORS(DefaultCORSConfig()))
	r.OPTIONS("/", func(c *gin.Context) { c.Status(200) })

	req := httptest.NewRequest(http.MethodOptions, "/", nil)
	req.Header.Set("Origin", "https://x")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Errorf("preflight code = %d, want 204", w.Code)
	}
}

func TestCORSNilConfigDefaults(t *testing.T) {
	r := gin.New()
	r.Use(CORS(nil))
	r.GET("/", func(c *gin.Context) { c.Status(200) })
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Errorf("code=%d", w.Code)
	}
}

func TestSecurityHeaders(t *testing.T) {
	cfg := &config.Config{}
	cfg.Security.HSTSMaxAge = 100
	r := gin.New()
	r.Use(SecurityHeaders(cfg))
	r.GET("/", func(c *gin.Context) { c.Status(200) })
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	checks := map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"Referrer-Policy":        "strict-origin-when-cross-origin",
	}
	for h, want := range checks {
		if got := w.Header().Get(h); got != want {
			t.Errorf("%s = %q, want %q", h, got, want)
		}
	}

	// HSTS must be present and carry the configured max-age.
	if hsts := w.Header().Get("Strict-Transport-Security"); !strings.Contains(hsts, "max-age=100") {
		t.Errorf("HSTS = %q, want it to contain max-age=100", hsts)
	}

	// CSP must be present and lock down the key directives.
	csp := w.Header().Get("Content-Security-Policy")
	if csp == "" {
		t.Fatal("missing CSP header")
	}
	for _, directive := range []string{
		"default-src 'self'",
		"script-src 'self' 'unsafe-inline'",
		"style-src 'self' 'unsafe-inline'",
		"img-src 'self' data:",
		"connect-src 'self'",
		"frame-ancestors 'none'",
		"base-uri 'self'",
		"form-action 'self'",
	} {
		if !strings.Contains(csp, directive) {
			t.Errorf("CSP %q missing directive %q", csp, directive)
		}
	}
}

func TestRequireAdmin(t *testing.T) {
	newReq := func(setup func(*gin.Context)) int {
		r := gin.New()
		r.Use(func(c *gin.Context) { setup(c); c.Next() })
		r.GET("/", RequireAdmin(), func(c *gin.Context) { c.Status(200) })
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
		return w.Code
	}

	if code := newReq(func(c *gin.Context) {}); code != http.StatusUnauthorized {
		t.Errorf("no user: code=%d want 401", code)
	}
	if code := newReq(func(c *gin.Context) { c.Set("user", &models.User{Role: "user"}) }); code != http.StatusForbidden {
		t.Errorf("non-admin: code=%d want 403", code)
	}
	if code := newReq(func(c *gin.Context) { c.Set("user", &models.User{Role: "admin"}) }); code != http.StatusOK {
		t.Errorf("admin: code=%d want 200", code)
	}
}

func TestRateLimiterAllowsThenBlocks(t *testing.T) {
	rl := NewRateLimiter(0, 2) // burst 2, no refill
	t.Cleanup(rl.Stop)
	r := gin.New()
	r.Use(rl.Middleware())
	r.GET("/x", func(c *gin.Context) { c.Status(200) })

	do := func() int {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		req.RemoteAddr = "10.0.0.1:1234"
		r.ServeHTTP(w, req)
		return w.Code
	}
	if do() != 200 || do() != 200 {
		t.Fatal("first two requests within burst should pass")
	}
	if code := do(); code != http.StatusTooManyRequests {
		t.Errorf("third request code=%d want 429", code)
	}
}

func TestGetCurrentUser(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	if _, ok := GetCurrentUser(c); ok {
		t.Error("expected no user")
	}
	u := &models.User{Role: "admin"}
	c.Set("user", u)
	got, ok := GetCurrentUser(c)
	if !ok || got != u {
		t.Error("expected user to be returned")
	}
}
