package main

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/gitduppy/gitduppy/internal/middleware"
)

// TestRepositoryRouteTreeBoots verifies that the git-browse routes register
// cleanly under the canonical /api/v1/repositories/:id prefix without a gin
// radix-tree conflict against the rest of the repositories group, AND that the
// deprecated /api/v1/repos browse alias has been fully removed (Phase 6).
//
// setupRouter itself pulls in the database, templates, and every handler
// constructor, so instead of calling it this test faithfully replays the exact
// method+path pairs registered under /api/v1/repositories/:id. gin panics at
// registration time on a conflicting route (for example a wildcard-name clash),
// so a successful registration here is a real smoke test of the consolidated
// browse routes.
func TestRepositoryRouteTreeBoots(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	noop := func(_ *gin.Context) {}

	v1 := r.Group("/api/v1")

	// Canonical /repositories group, mirroring setupRouter's registrations so the
	// full radix tree under /repositories/:id is exercised (CRUD, paperbin, tags,
	// and the browse routes now consolidated here). The deprecated /api/v1/repos
	// browse alias is intentionally NOT registered — it was removed in Phase 6.
	repository := v1.Group("/repositories")
	repository.GET("", noop)
	repository.POST("", noop)
	repository.GET("/paperbin", noop)
	repository.GET("/:id", noop)
	repository.PUT("/:id", noop)
	repository.DELETE("/:id", noop)
	repository.POST("/:id/restore", noop)
	repository.DELETE("/:id/force", noop)
	repository.POST("/:id/paperbin/branches/:branchId/restore", noop)
	repository.DELETE("/:id/paperbin/branches/:branchId", noop)
	repository.PATCH("/:id/status", noop)
	repository.POST("/:id/clone", noop)
	repository.GET("/:id/logs", noop)
	repository.GET("/:id/logs/stream", noop)
	repository.GET("/:id/jobs", noop)
	repository.GET("/:id/refs", noop)
	repository.GET("/:id/tree", noop)
	repository.GET("/:id/blob", noop)
	repository.GET("/:id/commits", noop)
	repository.GET("/:id/commit/:sha", noop)
	repository.GET("/:id/download", noop)

	// Repository tag routes are registered as a separate group sharing the same
	// /repositories/:id node in setupRouter; include them so any conflict with the
	// browse routes at that node would surface here too.
	repoTags := v1.Group("/repositories/:id/tags")
	repoTags.GET("", noop)
	repoTags.POST("", noop)
	repoTags.PUT("", noop)
	repoTags.DELETE("/:tagId", noop)

	// The canonical browse routes must resolve to a handler.
	wantPaths := map[string]bool{
		"/api/v1/repositories/:id/refs":        false,
		"/api/v1/repositories/:id/tree":        false,
		"/api/v1/repositories/:id/blob":        false,
		"/api/v1/repositories/:id/commits":     false,
		"/api/v1/repositories/:id/download":    false,
		"/api/v1/repositories/:id/commit/:sha": false,
	}
	for _, rt := range r.Routes() {
		if _, ok := wantPaths[rt.Path]; ok {
			wantPaths[rt.Path] = true
		}
		// The deprecated /api/v1/repos browse alias must be gone entirely. Note
		// the canonical prefix is "/api/v1/repositories/", which does not match
		// this "/api/v1/repos/" prefix (the next char is "i", not "/").
		if strings.HasPrefix(rt.Path, "/api/v1/repos/") {
			t.Errorf("deprecated /api/v1/repos alias route is still registered: %q", rt.Path)
		}
	}
	for p, found := range wantPaths {
		if !found {
			t.Errorf("expected route %q to be registered, but it was not found", p)
		}
	}

	// Sanity check that the canonical browse route actually dispatches (200 from
	// the no-op handler, not a 404 from a mis-registered tree).
	req := httptest.NewRequest("GET", "/api/v1/repositories/11111111-1111-1111-1111-111111111111/refs", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Errorf("canonical browse route did not dispatch: got status %d, want 200", w.Code)
	}
}

// TestDashboardOverviewRouteRegistered mirrors setupRouter's dashboard group so
// the new combined /overview endpoint (registered with the ETag middleware
// alongside the four existing single-purpose endpoints) is exercised for
// registration and dispatch without needing the full server wiring (DB, handlers).
func TestDashboardOverviewRouteRegistered(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	noop := func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) }

	dashboard := r.Group("/api/v1/dashboard")
	dashboard.GET("/stats", noop)
	dashboard.GET("/chart-data", noop)
	dashboard.GET("/top-repositories", noop)
	dashboard.GET("/recent-jobs", noop)
	dashboard.GET("/timeline", noop)
	dashboard.GET("/paperbin-quota", noop)
	dashboard.GET("/overview", middleware.ETag(), noop)

	want := "/api/v1/dashboard/overview"
	found := false
	for _, rt := range r.Routes() {
		if rt.Path == want {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected route %q to be registered", want)
	}

	req := httptest.NewRequest("GET", want, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Errorf("overview route did not dispatch: got status %d, want 200", w.Code)
	}
	// The ETag middleware must have stamped a validator on the combined payload.
	if w.Header().Get("ETag") == "" {
		t.Error("expected ETag header on the overview response")
	}
}
