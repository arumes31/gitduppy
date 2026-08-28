package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestCSRFMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(NewCSRFMiddleware(strings.Repeat("k", 32), false).Middleware())
	engine.GET("/api/v1/csrf-token", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"token": GetCSRFToken(c)})
	})
	engine.POST("/api/v1/change", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	engine.POST("/api/v1/webhooks/receive", func(c *gin.Context) { c.Status(http.StatusNoContent) })

	getRequest := httptest.NewRequest(http.MethodGet, "/api/v1/csrf-token", nil)
	getResponse := httptest.NewRecorder()
	engine.ServeHTTP(getResponse, getRequest)
	if getResponse.Code != http.StatusOK {
		t.Fatalf("token response status = %d, want %d", getResponse.Code, http.StatusOK)
	}
	var tokenResponse struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(getResponse.Body.Bytes(), &tokenResponse); err != nil {
		t.Fatalf("decode token response: %v", err)
	}
	if tokenResponse.Token == "" {
		t.Fatal("CSRF token must not be empty")
	}
	cookies := getResponse.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("CSRF middleware must set its paired cookie")
	}

	withoutToken := httptest.NewRequest(http.MethodPost, "/api/v1/change", nil)
	withoutTokenResponse := httptest.NewRecorder()
	engine.ServeHTTP(withoutTokenResponse, withoutToken)
	if withoutTokenResponse.Code != http.StatusForbidden {
		t.Fatalf("request without token status = %d, want %d", withoutTokenResponse.Code, http.StatusForbidden)
	}

	withToken := httptest.NewRequest(http.MethodPost, "/api/v1/change", nil)
	for _, cookie := range cookies {
		withToken.AddCookie(cookie)
	}
	withToken.Header.Set("X-CSRF-Token", tokenResponse.Token)
	withTokenResponse := httptest.NewRecorder()
	engine.ServeHTTP(withTokenResponse, withToken)
	if withTokenResponse.Code != http.StatusNoContent {
		t.Fatalf("request with token status = %d, want %d; body=%q cookies=%v token=%q", withTokenResponse.Code, http.StatusNoContent, withTokenResponse.Body.String(), cookies, tokenResponse.Token)
	}

	withBearer := httptest.NewRequest(http.MethodPost, "/api/v1/change", nil)
	withBearer.Header.Set("Authorization", "Bearer api-key")
	withBearerResponse := httptest.NewRecorder()
	engine.ServeHTTP(withBearerResponse, withBearer)
	if withBearerResponse.Code != http.StatusNoContent {
		t.Fatalf("bearer request status = %d, want %d", withBearerResponse.Code, http.StatusNoContent)
	}

	bearerWithSession := httptest.NewRequest(http.MethodPost, "/api/v1/change", nil)
	bearerWithSession.Header.Set("Authorization", "Bearer invalid-api-key")
	bearerWithSession.AddCookie(&http.Cookie{Name: "session", Value: "ambient-session"})
	bearerWithSessionResponse := httptest.NewRecorder()
	engine.ServeHTTP(bearerWithSessionResponse, bearerWithSession)
	if bearerWithSessionResponse.Code != http.StatusForbidden {
		t.Fatalf("bearer request with ambient session status = %d, want %d", bearerWithSessionResponse.Code, http.StatusForbidden)
	}

	incomingWebhook := httptest.NewRequest(http.MethodPost, "/api/v1/webhooks/receive", nil)
	incomingWebhookResponse := httptest.NewRecorder()
	engine.ServeHTTP(incomingWebhookResponse, incomingWebhook)
	if incomingWebhookResponse.Code != http.StatusNoContent {
		t.Fatalf("incoming webhook status = %d, want %d", incomingWebhookResponse.Code, http.StatusNoContent)
	}
}
