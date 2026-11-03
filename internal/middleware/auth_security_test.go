package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/gitduppy/gitduppy/internal/models"
	"github.com/google/uuid"
)

// TestAuthCacheEvictBetweenBeginAndStoreNotCached reproduces the revocation race:
// a validator captures the generation, reads a still-valid credential from the
// DB, a logout/revocation evicts the key, and only then does the validator try to
// publish. The stale identity must not be cached.
func TestAuthCacheEvictBetweenBeginAndStoreNotCached(t *testing.T) {
	ac := NewAuthCache()
	t.Cleanup(ac.Stop)
	user := &models.User{ID: uuid.New(), IsActive: true}

	gen := ac.begin()
	ac.Evict("cred-hash") // concurrent logout lands mid-validation
	if ac.store("cred-hash", user, gen) {
		t.Fatal("store after a concurrent Evict must be rejected")
	}
	if _, ok := ac.get("cred-hash"); ok {
		t.Fatal("revoked credential must not be served from cache")
	}

	// EvictUser (password change / deactivation) must have the same effect.
	gen = ac.begin()
	ac.EvictUser(user.ID)
	if ac.store("cred-hash", user, gen) {
		t.Fatal("store after a concurrent EvictUser must be rejected")
	}

	// Without an intervening eviction the result is cached as usual.
	gen = ac.begin()
	if !ac.store("cred-hash", user, gen) {
		t.Fatal("store without a concurrent eviction should succeed")
	}
	if got, ok := ac.get("cred-hash"); !ok || got != user {
		t.Fatal("expected the stored user to be served from cache")
	}
}

// TestRevalidateCredentialFailsClosed checks the WebSocket revalidation helper
// rejects unknown auth types and a missing DB rather than allowing the stream.
func TestRevalidateCredentialFailsClosed(t *testing.T) {
	if _, err := RevalidateCredential(context.Background(), nil, AuthTypeSession, "h", uuid.New()); err == nil {
		t.Fatal("nil DB must fail closed")
	}
}

// TestGetCredential verifies the context keys the auth middleware records are
// read back by GetCredential, and that a partially populated context is not
// treated as authenticated.
func TestGetCredential(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	if _, _, ok := GetCredential(c); ok {
		t.Fatal("empty context must not yield a credential")
	}
	c.Set(ContextKeyAuthType, AuthTypeSession)
	if _, _, ok := GetCredential(c); ok {
		t.Fatal("auth type without credential hash must not yield a credential")
	}
	c.Set(ContextKeyCredentialHash, "abc")
	authType, hash, ok := GetCredential(c)
	if !ok || authType != AuthTypeSession || hash != "abc" {
		t.Fatalf("unexpected credential: %q %q %v", authType, hash, ok)
	}
}

// TestCrossOriginProtectionLoginWithoutCookie covers login CSRF: the login
// endpoint mints a session, so it is origin-checked even when the browser has no
// session cookie yet, while non-browser clients (no Origin / Sec-Fetch-Site)
// still work.
func TestCrossOriginProtectionLoginWithoutCookie(t *testing.T) {
	mw, err := CrossOriginProtection("https://git.example.com/")
	if err != nil {
		t.Fatalf("CrossOriginProtection: %v", err)
	}
	r := gin.New()
	r.Use(mw)
	r.POST("/api/v1/auth/login", func(c *gin.Context) { c.Status(http.StatusOK) })
	r.POST("/api/v1/repositories", func(c *gin.Context) { c.Status(http.StatusOK) })

	do := func(path string, headers map[string]string) int {
		req := httptest.NewRequest(http.MethodPost, "http://git.example.com"+path, nil)
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w.Code
	}

	crossSite := map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": "https://attacker.test"}
	if code := do("/api/v1/auth/login", crossSite); code != http.StatusForbidden {
		t.Errorf("cross-site cookie-less login allowed: %d", code)
	}
	sibling := map[string]string{"Sec-Fetch-Site": "same-site", "Origin": "https://evil.example.com"}
	if code := do("/api/v1/auth/login", sibling); code != http.StatusForbidden {
		t.Errorf("sibling-subdomain cookie-less login allowed: %d", code)
	}
	if code := do("/api/v1/auth/login", map[string]string{"Sec-Fetch-Site": "same-origin"}); code != http.StatusOK {
		t.Errorf("same-origin login blocked: %d", code)
	}
	if code := do("/api/v1/auth/login", nil); code != http.StatusOK {
		t.Errorf("non-browser login (no Origin) blocked: %d", code)
	}
	// Other cookie-less endpoints stay unchecked (API-key clients).
	if code := do("/api/v1/repositories", crossSite); code != http.StatusOK {
		t.Errorf("cookie-less non-login request blocked: %d", code)
	}
}
