package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// WebHandler handles web UI requests.
type WebHandler struct {
}

// NewWebHandler creates a new web handler.
func NewWebHandler() *WebHandler {
	return &WebHandler{}
}

// Index redirects to dashboard
func (h *WebHandler) Index(c *gin.Context) {
	c.Redirect(http.StatusFound, "/dashboard")
}

// Login renders the login page
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

// Dashboard renders the dashboard page
func (h *WebHandler) Dashboard(c *gin.Context) {
	c.HTML(http.StatusOK, "dashboard.html", gin.H{
		"title": "Dashboard - GitDuppy",
	})
}

// Config renders the configuration page
func (h *WebHandler) Config(c *gin.Context) {
	c.HTML(http.StatusOK, "config.html", gin.H{
		"title": "Settings - GitDuppy",
	})
}
