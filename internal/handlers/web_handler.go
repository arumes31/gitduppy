package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// WebHandler handles web UI requests.
type WebHandler struct {
	// version is the build version string, injected into every rendered page as
	// `.version` so the base template can append ?v=<version> to the app.js/CSS
	// references and bust browser caches on each deploy.
	version string
}

// NewWebHandler creates a new web handler. version is the build version used for
// static-asset cache busting in templates.
func NewWebHandler(version string) *WebHandler {
	return &WebHandler{version: version}
}

// Index redirects to dashboard.
func (h *WebHandler) Index(c *gin.Context) {
	c.Redirect(http.StatusFound, "/dashboard")
}

// Login renders the login page.
func (h *WebHandler) Login(c *gin.Context) {
	// If already logged in, redirect to dashboard
	if _, exists := c.Get("user"); exists {
		c.Redirect(http.StatusFound, "/dashboard")
		return
	}
	c.HTML(http.StatusOK, "login.html", gin.H{
		"title":   "Login - GitDuppy",
		"version": h.version,
	})
}

// Dashboard renders the dashboard page.
func (h *WebHandler) Dashboard(c *gin.Context) {
	user, _ := c.Get("user")
	c.HTML(http.StatusOK, "dashboard.html", gin.H{
		"title":   "Dashboard - GitDuppy",
		"user":    user,
		"version": h.version,
	})
}

// Config renders the configuration page.
func (h *WebHandler) Config(c *gin.Context) {
	user, _ := c.Get("user")
	c.HTML(http.StatusOK, "config.html", gin.H{
		"title":   "Settings - GitDuppy",
		"user":    user,
		"version": h.version,
	})
}

// RepoList renders the repository list page.
func (h *WebHandler) RepoList(c *gin.Context) {
	user, _ := c.Get("user")
	c.HTML(http.StatusOK, "repos.html", gin.H{
		"title":   "Repositories - GitDuppy",
		"user":    user,
		"version": h.version,
	})
}

// RepoDetail renders the repository browser page.
func (h *WebHandler) RepoDetail(c *gin.Context) {
	user, _ := c.Get("user")
	c.HTML(http.StatusOK, "repo_detail.html", gin.H{
		"title":   "Browse Repository - GitDuppy",
		"user":    user,
		"repoID":  c.Param("id"),
		"version": h.version,
	})
}

// RepoCommit renders a single commit detail page.
func (h *WebHandler) RepoCommit(c *gin.Context) {
	user, _ := c.Get("user")
	c.HTML(http.StatusOK, "repo_commit.html", gin.H{
		"title":   "Commit - GitDuppy",
		"user":    user,
		"repoID":  c.Param("id"),
		"sha":     c.Param("sha"),
		"version": h.version,
	})
}

// Search renders the global code search page.
func (h *WebHandler) Search(c *gin.Context) {
	user, _ := c.Get("user")
	c.HTML(http.StatusOK, "search.html", gin.H{
		"title":   "Search - GitDuppy",
		"user":    user,
		"version": h.version,
	})
}
