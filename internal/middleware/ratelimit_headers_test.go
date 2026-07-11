package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// newTestEngine wires the rate-limit middleware in front of a trivial 200
// handler so header behavior can be asserted end-to-end.
func newTestEngine(rl *RateLimiter) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(rl.Middleware())
	r.GET("/api/v1/repositories", func(c *gin.Context) { c.Status(http.StatusOK) })
	return r
}

func doGet(r *gin.Engine) *httptest.ResponseRecorder {
	// A fixed remote address keeps every request on the same per-client bucket.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/repositories", nil)
	req.RemoteAddr = "203.0.113.7:5555"
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// TestRateLimitHeadersOnAllow checks that an accepted request advertises the
// tier limit and the remaining token budget.
func TestRateLimitHeadersOnAllow(t *testing.T) {
	rl := NewRateLimiter(1, 5) // 1 rps, burst 5
	defer rl.Stop()
	r := newTestEngine(rl)

	w := doGet(r)
	if w.Code != http.StatusOK {
		t.Fatalf("first request status = %d, want 200", w.Code)
	}
	if got := w.Header().Get("X-RateLimit-Limit"); got != "5" {
		t.Errorf("X-RateLimit-Limit = %q, want %q", got, "5")
	}
	// One of five tokens was consumed, so four remain.
	if got := w.Header().Get("X-RateLimit-Remaining"); got != "4" {
		t.Errorf("X-RateLimit-Remaining = %q, want %q", got, "4")
	}
}

// TestRateLimitHeadersOn429 checks that an exhausted bucket returns 429 with a
// Retry-After and the tier limit, and no negative/empty backoff.
func TestRateLimitHeadersOn429(t *testing.T) {
	// Burst of 1 with a slow refill: the first request drains the bucket, the
	// second is rejected.
	rl := NewRateLimiter(0.001, 1)
	defer rl.Stop()
	r := newTestEngine(rl)

	if w := doGet(r); w.Code != http.StatusOK {
		t.Fatalf("first request status = %d, want 200", w.Code)
	}

	w := doGet(r)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("second request status = %d, want 429", w.Code)
	}
	retry := w.Header().Get("Retry-After")
	if retry == "" {
		t.Fatal("429 response is missing Retry-After header")
	}
	if retry == "0" || retry[0] == '-' {
		t.Errorf("Retry-After = %q, want a positive whole number of seconds", retry)
	}
	if got := w.Header().Get("X-RateLimit-Limit"); got != "1" {
		t.Errorf("X-RateLimit-Limit = %q, want %q", got, "1")
	}
}
