package handlers_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/gitduppy/gitduppy/internal/handlers"
	"github.com/stretchr/testify/assert"
)

func TestGlobalSearchValidation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()

	repoHandler := handlers.NewRepositoryHandler(nil, nil, nil, nil, nil)
	r.GET("/api/v1/search", repoHandler.GlobalSearch)

	t.Run("missing query parameter", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodGet, "/api/v1/search", nil)
		resp := httptest.NewRecorder()
		r.ServeHTTP(resp, req)

		assert.Equal(t, http.StatusBadRequest, resp.Code)
		assert.Contains(t, resp.Body.String(), "MISSING_QUERY")
	})

	t.Run("query too short", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodGet, "/api/v1/search?q=ab", nil)
		resp := httptest.NewRecorder()
		r.ServeHTTP(resp, req)

		assert.Equal(t, http.StatusBadRequest, resp.Code)
		assert.Contains(t, resp.Body.String(), "QUERY_TOO_SHORT")
	})
}
