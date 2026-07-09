package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// TestETagConditionalRequest exercises the full flow: a first request returns 200
// with an ETag and a body; a second request carrying that ETag in If-None-Match
// returns 304 with an empty body and the ETag preserved.
func TestETagConditionalRequest(t *testing.T) {
	r := gin.New()
	r.GET("/x", ETag(), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"hello": "world", "n": 42})
	})

	// First request: no validator sent.
	w1 := httptest.NewRecorder()
	r.ServeHTTP(w1, httptest.NewRequest(http.MethodGet, "/x", nil))
	if w1.Code != http.StatusOK {
		t.Fatalf("first request code = %d, want 200", w1.Code)
	}
	etag := w1.Header().Get("ETag")
	if etag == "" {
		t.Fatal("first response is missing an ETag header")
	}
	if w1.Body.Len() == 0 {
		t.Fatal("first response body should not be empty")
	}

	// Second request: send the returned ETag back.
	w2 := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("If-None-Match", etag)
	r.ServeHTTP(w2, req)
	if w2.Code != http.StatusNotModified {
		t.Fatalf("conditional request code = %d, want 304", w2.Code)
	}
	if w2.Body.Len() != 0 {
		t.Fatalf("304 response body should be empty, got %d bytes", w2.Body.Len())
	}
	if got := w2.Header().Get("ETag"); got != etag {
		t.Errorf("304 ETag = %q, want %q", got, etag)
	}
}

// TestETagMismatchReturnsBody verifies a stale/non-matching validator still yields
// the full 200 body.
func TestETagMismatchReturnsBody(t *testing.T) {
	r := gin.New()
	r.GET("/x", ETag(), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"v": 1})
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("If-None-Match", `W/"stale"`)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200", w.Code)
	}
	if w.Body.Len() == 0 {
		t.Fatal("expected full body on ETag mismatch")
	}
}
