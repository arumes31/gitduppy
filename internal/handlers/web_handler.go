package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// WebHandler handles web UI requests.
type WebHandler struct{}

// NewWebHandler creates a new web handler.
func NewWebHandler() *WebHandler {
	return &WebHandler{}
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
		"title": "Login - GitDuppy",
	})
}

// Dashboard renders the dashboard page.
func (h *WebHandler) Dashboard(c *gin.Context) {
	user, _ := c.Get("user")
	c.HTML(http.StatusOK, "dashboard.html", gin.H{
		"title": "Dashboard - GitDuppy",
		"user":  user,
	})
}

// Config renders the configuration page.
func (h *WebHandler) Config(c *gin.Context) {
	user, _ := c.Get("user")
	c.HTML(http.StatusOK, "config.html", gin.H{
		"title": "Settings - GitDuppy",
		"user":  user,
	})
}

// RepoList renders the repository list page.
func (h *WebHandler) RepoList(c *gin.Context) {
	user, _ := c.Get("user")
	c.HTML(http.StatusOK, "repos.html", gin.H{
		"title": "Repositories - GitDuppy",
		"user":  user,
	})
}

// RepoDetail renders the repository browser page.
func (h *WebHandler) RepoDetail(c *gin.Context) {
	user, _ := c.Get("user")
	c.HTML(http.StatusOK, "repo_detail.html", gin.H{
		"title":  "Browse Repository - GitDuppy",
		"user":   user,
		"repoID": c.Param("id"),
	})
}

// RepoCommit renders a single commit detail page.
func (h *WebHandler) RepoCommit(c *gin.Context) {
	user, _ := c.Get("user")
	c.HTML(http.StatusOK, "repo_commit.html", gin.H{
		"title":  "Commit - GitDuppy",
		"user":   user,
		"repoID": c.Param("id"),
		"sha":    c.Param("sha"),
	})
}
