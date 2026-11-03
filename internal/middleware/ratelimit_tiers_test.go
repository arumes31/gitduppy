package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestLimitForCategories(t *testing.T) {
	rl := &RateLimiter{rps: 60, burst: 60}

	cases := []struct {
		path       string
		wantPrefix string
	}{
		{"/api/v1/auth/login", "auth:"},
		{"/api/v1/oauth/github/login", "auth:"},
		{"/api/v1/search", "expensive:"},
		{"/api/v1/dashboard/stats", "dashboard:"},
		{"/api/v1/repositories", "api:"},
		{"/api/v1/health", "api:"},
	}
	for _, c := range cases {
		prefix, rps, burst := rl.limitFor(c.path)
		if prefix != c.wantPrefix {
			t.Errorf("limitFor(%q) prefix = %q, want %q", c.path, prefix, c.wantPrefix)
		}
		// Auth and expensive tiers must be strictly tighter than the default API tier.
		switch c.wantPrefix {
		case "auth:":
			if rps >= rl.rps {
				t.Errorf("auth tier rps %v should be below default %v", rps, rl.rps)
			}
		case "expensive:":
			if rps >= rl.rps {
				t.Errorf("expensive tier rps %v should be below default %v", rps, rl.rps)
			}
		case "dashboard:":
			// The dashboard is isolated on its own key but must stay generous:
			// a single view fans out to several of these endpoints, so it uses
			// the full API budget rather than a throttled fraction of it.
			if rps != rl.rps || burst != rl.burst {
				t.Errorf("dashboard tier should use full API budget, got rps=%v burst=%v", rps, burst)
			}
		case "api:":
			if rps != rl.rps || burst != rl.burst {
				t.Errorf("default tier should use base limits, got rps=%v burst=%v", rps, burst)
			}
		}
		if burst < 1 {
			t.Errorf("burst for %q must be at least 1, got %d", c.path, burst)
		}
	}
}

// TestAuthBudgetOverride checks that the configured auth budget
// (security.rate_limit.auth_requests_per_minute) is what the auth tier uses,
// independent of the API budget.
func TestAuthBudgetOverride(t *testing.T) {
	rl := &RateLimiter{rps: 60, burst: 60, auth: RateBudget{RPS: 0.5, Burst: 2}}
	prefix, rps, burst := rl.limitFor("/api/v1/auth/login")
	if prefix != "auth:" || rps != 0.5 || burst != 2 {
		t.Fatalf("auth tier = (%q, %v, %d), want (auth:, 0.5, 2)", prefix, rps, burst)
	}
	// Other tiers are unaffected by the auth override.
	if _, rps, burst := rl.limitFor("/api/v1/repositories"); rps != 60 || burst != 60 {
		t.Errorf("api tier changed by auth override: rps=%v burst=%d", rps, burst)
	}
}

// TestAuthBudgetChangesAdmission drives the middleware: with a generous API
// budget but a restrictive auth budget, the third login attempt is rejected
// while ordinary API calls from the same client still pass.
func TestAuthBudgetChangesAdmission(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rl := NewRateLimiter(PerMinute(600), RateBudget{RPS: 0.0001, Burst: 2})
	defer rl.Stop()
	r := gin.New()
	r.Use(rl.Middleware())
	r.POST("/api/v1/auth/login", func(c *gin.Context) { c.Status(http.StatusOK) })
	r.GET("/api/v1/repositories", func(c *gin.Context) { c.Status(http.StatusOK) })

	do := func(method, path string) int {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(method, path, nil)
		req.RemoteAddr = "10.0.0.9:1234"
		r.ServeHTTP(w, req)
		return w.Code
	}
	for i := 0; i < 2; i++ {
		if code := do(http.MethodPost, "/api/v1/auth/login"); code != http.StatusOK {
			t.Fatalf("login %d: code=%d want 200", i+1, code)
		}
	}
	if code := do(http.MethodPost, "/api/v1/auth/login"); code != http.StatusTooManyRequests {
		t.Errorf("third login: code=%d want 429 (auth budget burst 2)", code)
	}
	if code := do(http.MethodGet, "/api/v1/repositories"); code != http.StatusOK {
		t.Errorf("api call: code=%d want 200 (separate budget)", code)
	}

	// The default API-derived auth budget (600/6 = burst 100) would have admitted
	// all three logins, proving the override is what changed admission.
	if _, _, burst := (&RateLimiter{rps: 10, burst: 600}).limitFor("/api/v1/auth/login"); burst != 100 {
		t.Errorf("fallback auth burst = %d, want 100", burst)
	}
}

func TestPerMinute(t *testing.T) {
	b := PerMinute(30)
	if b.RPS != 0.5 || b.Burst != 30 {
		t.Errorf("PerMinute(30) = %+v, want {0.5 30}", b)
	}
}
