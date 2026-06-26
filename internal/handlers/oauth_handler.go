package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gitduppy/gitduppy/internal/middleware"
	"github.com/gitduppy/gitduppy/internal/models"
	"github.com/gitduppy/gitduppy/internal/services"
	"github.com/gitduppy/gitduppy/pkg/response"
	"github.com/google/uuid"
	"golang.org/x/oauth2"
)

// OAuthHandler handles OAuth2/OIDC requests.
type OAuthHandler struct {
	oauthService *services.OAuthService
	authService  *services.AuthService
}

// NewOAuthHandler creates a new OAuth handler.
func NewOAuthHandler(oauthService *services.OAuthService, authService *services.AuthService) *OAuthHandler {
	return &OAuthHandler{
		oauthService: oauthService,
		authService:  authService,
	}
}

// LoginWithProvider handles GET /api/v1/oauth/:provider/login.
func (h *OAuthHandler) LoginWithProvider(c *gin.Context) {
	provider := c.Param("provider")
	oauthProvider := services.OAuthProvider(provider)

	oauthConfig, err := h.oauthService.GetOAuthConfig(c, oauthProvider)
	if err != nil {
		response.BadRequest(c, "OAUTH_NOT_CONFIGURED", err.Error())
		return
	}

	// Generate state parameter to prevent CSRF
	state := uuid.New().String()
	c.SetCookie("oauth_state", state, 3600, "/", "", false, true)

	url := oauthConfig.AuthCodeURL(state, oauth2.AccessTypeOffline)
	c.Redirect(http.StatusFound, url)
}

// Callback handles GET /api/v1/oauth/:provider/callback.
func (h *OAuthHandler) Callback(c *gin.Context) {
	provider := c.Param("provider")
	oauthProvider := services.OAuthProvider(provider)

	// Verify state parameter
	stateCookie, err := c.Cookie("oauth_state")
	if err != nil {
		response.BadRequest(c, "INVALID_STATE", "Invalid or missing state parameter")
		return
	}
	c.SetCookie("oauth_state", "", -1, "/", "", false, true)

	state := c.Query("state")
	if state != stateCookie {
		response.BadRequest(c, "INVALID_STATE", "State parameter mismatch")
		return
	}

	// Get OAuth config
	oauthConfig, err := h.oauthService.GetOAuthConfig(c, oauthProvider)
	if err != nil {
		response.BadRequest(c, "OAUTH_NOT_CONFIGURED", err.Error())
		return
	}

	// Exchange code for token
	code := c.Query("code")
	token, err := oauthConfig.Exchange(context.Background(), code)
	if err != nil {
		response.BadRequest(c, "OAUTH_ERROR", "Failed to exchange code for token: "+err.Error())
		return
	}

	// Get user email from provider
	email, err := h.oauthService.GetUserEmailFromProvider(c, oauthProvider, token)
	if err != nil {
		response.BadRequest(c, "OAUTH_ERROR", "Failed to get user email: "+err.Error())
		return
	}

	// Extract username (use email prefix as fallback)
	username := email
	if idx := strings.Index(email, "@"); idx > 0 {
		username = email[:idx]
	}

	// Create or update user from OAuth data
	user, err := h.oauthService.CreateOrUpdateUserFromOAuth(c, oauthProvider, token.AccessToken, email, username)
	if err != nil {
		response.InternalError(c, "Failed to create/update user: "+err.Error())
		return
	}

	// Create session
	sessionToken, err := h.authService.GenerateSessionToken()
	if err != nil {
		response.InternalError(c, "Failed to generate session token: "+err.Error())
		return
	}
	expiresAt := time.Now().Add(h.authService.SessionDuration())

	session := &models.Session{
		Token:  sessionToken,
		UserID: user.ID,
		Data:   `{"auth_type":"oauth","provider":"` + string(oauthProvider) + `"}`,
		Expiry: expiresAt,
	}
	if err := h.authService.DB().Create(session).Error; err != nil {
		response.InternalError(c, "Failed to create session: "+err.Error())
		return
	}

	// Set session cookie
	c.SetCookie("session", sessionToken, 86400, "/", "", false, true)

	// Redirect to frontend or return success
	if c.Query("redirect") != "" {
		c.Redirect(http.StatusFound, c.Query("redirect"))
	} else {
		response.SuccessWithMessage(c, "Login successful", gin.H{
			"user":          user,
			"session_token": sessionToken,
			"expires_at":    expiresAt,
		})
	}
}

// LinkAccount handles POST /api/v1/oauth/:provider/link.
func (h *OAuthHandler) LinkAccount(c *gin.Context) {
	_, ok := middleware.GetCurrentUser(c)
	if !ok {
		response.Unauthorized(c, "Not authenticated")
		return
	}

	provider := c.Param("provider")
	oauthProvider := services.OAuthProvider(provider)

	oauthConfig, err := h.oauthService.GetOAuthConfig(c, oauthProvider)
	if err != nil {
		response.BadRequest(c, "OAUTH_NOT_CONFIGURED", err.Error())
		return
	}

	// Generate state parameter
	state := uuid.New().String()
	c.SetCookie("oauth_link_state", state, 3600, "/", "", false, true)

	url := oauthConfig.AuthCodeURL(state, oauth2.AccessTypeOffline)
	c.JSON(http.StatusOK, gin.H{"auth_url": url})
}

// LinkCallback handles GET /api/v1/oauth/:provider/link/callback.
func (h *OAuthHandler) LinkCallback(c *gin.Context) {
	user, ok := middleware.GetCurrentUser(c)
	if !ok {
		response.Unauthorized(c, "Not authenticated")
		return
	}

	provider := c.Param("provider")
	oauthProvider := services.OAuthProvider(provider)

	// Verify state parameter
	stateCookie, err := c.Cookie("oauth_link_state")
	if err != nil {
		response.BadRequest(c, "INVALID_STATE", "Invalid or missing state parameter")
		return
	}
	c.SetCookie("oauth_link_state", "", -1, "/", "", false, true)

	state := c.Query("state")
	if state != stateCookie {
		response.BadRequest(c, "INVALID_STATE", "State parameter mismatch")
		return
	}

	oauthConfig, err := h.oauthService.GetOAuthConfig(c, oauthProvider)
	if err != nil {
		response.BadRequest(c, "OAUTH_NOT_CONFIGURED", err.Error())
		return
	}

	code := c.Query("code")
	token, err := oauthConfig.Exchange(context.Background(), code)
	if err != nil {
		response.BadRequest(c, "OAUTH_ERROR", "Failed to exchange code for token: "+err.Error())
		return
	}

	// Link OAuth account to existing user
	if err := h.oauthService.LinkOAuthAccount(c, user.ID, oauthProvider, token.AccessToken); err != nil {
		response.BadRequest(c, "LINK_ERROR", "Failed to link OAuth account: "+err.Error())
		return
	}

	response.SuccessWithMessage(c, "OAuth account linked successfully", nil)
}

// UnlinkAccount handles POST /api/v1/oauth/:provider/unlink.
func (h *OAuthHandler) UnlinkAccount(c *gin.Context) {
	user, ok := middleware.GetCurrentUser(c)
	if !ok {
		response.Unauthorized(c, "Not authenticated")
		return
	}

	provider := c.Param("provider")

	// Verify user has this OAuth provider linked
	if user.OAuthProvider == nil || *user.OAuthProvider != provider {
		response.BadRequest(c, "NOT_LINKED", "OAuth account not linked")
		return
	}

	// Unlink OAuth account
	user.OAuthProvider = nil
	user.OAuthSubject = nil
	if err := h.authService.DB().Save(user).Error; err != nil {
		response.InternalError(c, "Failed to unlink OAuth account: "+err.Error())
		return
	}

	response.SuccessWithMessage(c, "OAuth account unlinked successfully", nil)
}

// ManifestCallback handles GET /api/v1/oauth/github/manifest-callback.
// GitHub redirects here with a ?code=CODE parameter after manifest creation.
func (h *OAuthHandler) ManifestCallback(c *gin.Context) {
	code := c.Query("code")
	if code == "" {
		response.BadRequest(c, "INVALID_REQUEST", "Missing code parameter")
		return
	}

	// 1. Exchange manifest code for credentials
	apiURL := fmt.Sprintf("https://api.github.com/app-manifests/%s/conversions", code)
	req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodPost, apiURL, nil)
	if err != nil {
		c.Redirect(http.StatusFound, "/config?error=failed_to_create_exchange_request")
		return
	}

	// GitHub requires User-Agent header for all API calls
	req.Header.Set("User-Agent", "GitDuppy")
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.Redirect(http.StatusFound, "/config?error=failed_to_contact_github")
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		c.Redirect(http.StatusFound, fmt.Sprintf("/config?error=github_returned_status_%d", resp.StatusCode))
		return
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		c.Redirect(http.StatusFound, "/config?error=failed_to_read_response")
		return
	}

	// Decode credentials response
	var data struct {
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
	}
	if err := json.Unmarshal(body, &data); err != nil {
		c.Redirect(http.StatusFound, "/config?error=failed_to_decode_credentials")
		return
	}

	if data.ClientID == "" {
		c.Redirect(http.StatusFound, "/config?error=received_empty_client_id")
		return
	}

	// 2. Save credentials in database
	if err := h.oauthService.SaveGitHubCredentials(c.Request.Context(), data.ClientID, data.ClientSecret); err != nil {
		c.Redirect(http.StatusFound, "/config?error=failed_to_save_settings")
		return
	}

	// 3. Redirect back to configuration page with success indicator
	c.Redirect(http.StatusFound, "/config?success=github_setup")
}
