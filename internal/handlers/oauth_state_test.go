package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func TestOAuthStateStoreSingleUseAndExpiry(t *testing.T) {
	s := newOAuthStateStore()
	now := time.Now()
	s.now = func() time.Time { return now }

	user := uuid.New()
	state, err := s.issue(oauthStateRecord{provider: "github", userID: user, sessionHash: "h"})
	if err != nil {
		t.Fatal(err)
	}
	if len(state) < 40 {
		t.Fatalf("state too short to be unguessable: %q", state)
	}
	rec, ok := s.consume(state)
	if !ok || rec.userID != user || rec.provider != "github" || rec.sessionHash != "h" {
		t.Fatalf("unexpected record: %+v %v", rec, ok)
	}
	if _, ok := s.consume(state); ok {
		t.Fatal("state must be single-use")
	}
	if _, ok := s.consume("never-issued"); ok {
		t.Fatal("unknown state must be rejected")
	}

	expired, _ := s.issue(oauthStateRecord{provider: "github"})
	now = now.Add(oauthStateTTL + time.Second)
	if _, ok := s.consume(expired); ok {
		t.Fatal("expired state must be rejected")
	}
}

// callbackCtx builds a gin context for a callback carrying the given query state
// and (optionally) state cookie.
func callbackCtx(state, cookieName, cookieValue string) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/oauth/github/callback?state="+state, nil)
	if cookieName != "" {
		req.AddCookie(&http.Cookie{Name: cookieName, Value: cookieValue})
	}
	c.Request = req
	return c, w
}

func TestTakeOAuthState(t *testing.T) {
	prev := cookieSecure
	t.Cleanup(func() { cookieSecure = prev })
	cookieSecure = true // production: only the __Host- cookie is honored

	h := &OAuthHandler{states: newOAuthStateStore()}
	issue := func(provider string) string {
		s, err := h.states.issue(oauthStateRecord{provider: provider})
		if err != nil {
			t.Fatal(err)
		}
		return s
	}

	t.Run("valid", func(t *testing.T) {
		st := issue("github")
		c, _ := callbackCtx(st, oauthStateCookieHost, st)
		if _, ok := h.takeOAuthState(c, "github"); !ok {
			t.Fatal("valid state rejected")
		}
	})
	t.Run("planted unprefixed cookie ignored when secure", func(t *testing.T) {
		st := issue("github")
		c, w := callbackCtx(st, oauthStateCookieBase, st)
		if _, ok := h.takeOAuthState(c, "github"); ok {
			t.Fatal("unprefixed (sibling-plantable) cookie accepted in secure mode")
		}
		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", w.Code)
		}
	})
	t.Run("state without server-side record", func(t *testing.T) {
		c, _ := callbackCtx("forged", oauthStateCookieHost, "forged")
		if _, ok := h.takeOAuthState(c, "github"); ok {
			t.Fatal("state without an initiation record accepted")
		}
	})
	t.Run("provider mismatch", func(t *testing.T) {
		st := issue("gitlab")
		c, _ := callbackCtx(st, oauthStateCookieHost, st)
		if _, ok := h.takeOAuthState(c, "github"); ok {
			t.Fatal("record for another provider accepted")
		}
	})
	t.Run("cookie mismatch", func(t *testing.T) {
		st := issue("github")
		c, _ := callbackCtx(st, oauthStateCookieHost, "other")
		if _, ok := h.takeOAuthState(c, "github"); ok {
			t.Fatal("state not matching the browser cookie accepted")
		}
	})
}

func TestOAuthStateCookieName(t *testing.T) {
	prev := cookieSecure
	t.Cleanup(func() { cookieSecure = prev })

	cookieSecure = true
	c, w := callbackCtx("", "", "")
	setOAuthStateCookie(c, "v", 60)
	got := w.Header().Get("Set-Cookie")
	if !strings.HasPrefix(got, oauthStateCookieHost+"=v") || !strings.Contains(got, "Secure") ||
		!strings.Contains(got, "Path=/") || strings.Contains(got, "Domain=") {
		t.Fatalf("secure state cookie must satisfy __Host- rules, got %q", got)
	}

	cookieSecure = false
	c, w = callbackCtx("", "", "")
	setOAuthStateCookie(c, "v", 60)
	if got := w.Header().Get("Set-Cookie"); !strings.HasPrefix(got, oauthStateCookieBase+"=v") {
		t.Fatalf("plain-HTTP state cookie should use the unprefixed name, got %q", got)
	}
}

// TestLoginRequiresJSONContentType guards against login CSRF via simple
// cross-site form posts, which cannot set Content-Type: application/json.
func TestLoginRequiresJSONContentType(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := NewAuthHandler(nil, nil)
	r.POST("/api/v1/auth/login", h.Login)

	for _, ct := range []string{"text/plain", "application/x-www-form-urlencoded", ""} {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"username":"a","password":"b"}`))
		if ct != "" {
			req.Header.Set("Content-Type", ct)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusUnsupportedMediaType {
			t.Errorf("Content-Type %q: expected 415, got %d", ct, w.Code)
		}
	}
}
