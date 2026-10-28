package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/gitduppy/gitduppy/internal/models"
	"github.com/google/uuid"
)

func TestIsExcludedMatchesExactPathsOnly(t *testing.T) {
	m := &AuthMiddleware{excludePaths: []string{"/api/v1/health", "/api/v1/webhooks/receive"}}
	cases := map[string]bool{
		"/api/v1/health":                 true,
		"/api/v1/webhooks/receive":       true,
		"/api/v1/healthX":                false,
		"/api/v1/health/../users":        false,
		"/api/v1/webhooks/receive/extra": false,
	}
	for path, want := range cases {
		if got := m.isExcluded(path); got != want {
			t.Errorf("isExcluded(%q)=%v want %v", path, got, want)
		}
	}
}

func TestCrossOriginProtection(t *testing.T) {
	mw, err := CrossOriginProtection("https://git.example.com/")
	if err != nil {
		t.Fatalf("CrossOriginProtection: %v", err)
	}
	r := gin.New()
	r.Use(mw)
	r.POST("/api/v1/repositories", func(c *gin.Context) { c.Status(http.StatusOK) })

	do := func(cookie bool, headers map[string]string) int {
		req := httptest.NewRequest(http.MethodPost, "http://git.example.com/api/v1/repositories", nil)
		if cookie {
			req.AddCookie(&http.Cookie{Name: "session", Value: "tok"})
		}
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w.Code
	}

	if code := do(true, map[string]string{"Sec-Fetch-Site": "same-origin"}); code != http.StatusOK {
		t.Errorf("same-origin request blocked: %d", code)
	}
	if code := do(true, map[string]string{"Sec-Fetch-Site": "same-site", "Origin": "https://evil.example.com"}); code != http.StatusForbidden {
		t.Errorf("sibling-subdomain request allowed: %d", code)
	}
	if code := do(true, map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": "https://attacker.test"}); code != http.StatusForbidden {
		t.Errorf("cross-site request allowed: %d", code)
	}
	if code := do(true, map[string]string{"Origin": "https://git.example.com"}); code != http.StatusOK {
		t.Errorf("trusted base_url origin blocked: %d", code)
	}
	// No session cookie (API key clients): nothing to forge, so not checked.
	if code := do(false, map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": "https://attacker.test"}); code != http.StatusOK {
		t.Errorf("cookie-less request blocked: %d", code)
	}
}

func TestCanModifyRepo(t *testing.T) {
	owner := uuid.New()
	other := uuid.New()
	cases := []struct {
		name      string
		user      *models.User
		createdBy *uuid.UUID
		want      bool
		wantCode  int
	}{
		{"owner", &models.User{ID: owner, Role: "user"}, &owner, true, http.StatusOK},
		{"admin", &models.User{ID: other, Role: "admin"}, &owner, true, http.StatusOK},
		{"other user", &models.User{ID: other, Role: "user"}, &owner, false, http.StatusForbidden},
		{"no owner recorded", &models.User{ID: other, Role: "user"}, nil, false, http.StatusForbidden},
		{"anonymous", nil, &owner, false, http.StatusUnauthorized},
	}
	for _, tc := range cases {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		if tc.user != nil {
			c.Set("user", tc.user)
		}
		got := canModifyRepo(c, tc.createdBy)
		if got != tc.want {
			t.Errorf("%s: canModifyRepo=%v want %v", tc.name, got, tc.want)
		}
		if !got && w.Code != tc.wantCode {
			t.Errorf("%s: status=%d want %d", tc.name, w.Code, tc.wantCode)
		}
	}
}
