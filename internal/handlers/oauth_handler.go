package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gitduppy/gitduppy/internal/middleware"
	"github.com/gitduppy/gitduppy/internal/models"
	"github.com/gitduppy/gitduppy/internal/services"
	"github.com/gitduppy/gitduppy/pkg/crypto"
	"github.com/gitduppy/gitduppy/pkg/response"
	"github.com/google/uuid"
	"golang.org/x/oauth2"
)

// oauthExchangeTimeout bounds the OAuth authorization-code -> token exchange so a
// slow or hung provider cannot block the callback request.
const oauthExchangeTimeout = 15 * time.Second

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

// isSafeRedirect reports whether target is an app-local relative path that is
// safe to redirect to. It rejects absolute URLs and protocol-relative ("//")
// or backslash-prefixed values to prevent open redirects.
func isSafeRedirect(target string) bool {
	if target == "" || !strings.HasPrefix(target, "/") {
		return false
	}
	if strings.HasPrefix(target, "//") || strings.HasPrefix(target, "/\\") {
		return false
	}
	// Reject control characters (and backslashes) anywhere: browsers strip e.g.
	// a tab in "/\t/evil.com" and re-parse it as a protocol-relative
	// "//evil.com", turning it back into an open redirect.
	for _, r := range target {
		if r < 0x20 || r == 0x7f || r == '\\' {
			return false
		}
	}
	return true
}

// currentSessionUser resolves the user for the request's existing session cookie,
// or nil when there is no valid session.
func (h *OAuthHandler) currentSessionUser(c *gin.Context) *models.User {
	token, err := c.Cookie("session")
	if err != nil || token == "" {
		return nil
	}
	user, err := h.authService.ValidateSession(c, token)
	if err != nil {
		return nil
	}
	return user
}

// finishOAuthRedirect sends the browser to the post-login redirect target (query
// param or the oauth_redirect cookie), falling back to a JSON success response.
func (h *OAuthHandler) finishOAuthRedirect(c *gin.Context, body gin.H, message string) {
	redirectTarget := c.Query("redirect")
	if redirectTarget == "" {
		if cookie, cErr := c.Cookie("oauth_redirect"); cErr == nil && cookie != "" {
			redirectTarget = cookie
		}
	}
	c.SetCookie("oauth_redirect", "", -1, "/", "", requestIsHTTPS(c), true)
	if isSafeRedirect(redirectTarget) {
		c.Redirect(http.StatusFound, redirectTarget)
		return
	}
	response.SuccessWithMessage(c, message, body)
}

// maybeMirrorGitHubRepos, for GitHub logins, imports all of the user's GitHub
// repositories as mirrors in the background so the redirect is not delayed. It is
// best-effort; failures are logged.
func (h *OAuthHandler) maybeMirrorGitHubRepos(provider services.OAuthProvider, userID uuid.UUID, token *oauth2.Token) {
	if provider != services.GitHubProvider || token == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		n, err := h.oauthService.MirrorAllGitHubRepos(ctx, userID, token)
		if err != nil {
			log.Printf("github repo mirror import failed for user %s: %v", userID, err)
			return
		}
		if n > 0 {
			log.Printf("github repo mirror import: created %d repositories for user %s", n, userID)
		}
	}()
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
	c.SetCookie("oauth_state", state, 3600, "/", "", requestIsHTTPS(c), true)

	// Remember where to send the browser after a successful login. This makes
	// browser-initiated logins (including the automated App-setup flow) land on a
	// real page instead of a raw JSON response.
	redirectTarget := c.Query("redirect")
	if redirectTarget == "" && c.Query("setup") != "" {
		redirectTarget = "/dashboard?success=github_setup"
	}
	if isSafeRedirect(redirectTarget) {
		c.SetCookie("oauth_redirect", redirectTarget, 600, "/", "", requestIsHTTPS(c), true)
	}

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
	c.SetCookie("oauth_state", "", -1, "/", "", requestIsHTTPS(c), true)

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
	// Bound the token exchange with an explicit timeout so a slow or hung provider
	// cannot block the request for its full lifetime.
	exCtx, exCancel := context.WithTimeout(c.Request.Context(), oauthExchangeTimeout)
	defer exCancel()
	token, err := oauthConfig.Exchange(exCtx, code)
	if err != nil {
		response.BadRequest(c, "OAUTH_ERROR", "Failed to exchange code for token: "+err.Error())
		return
	}

	// Get user email and stable subject from provider
	email, subject, err := h.oauthService.GetUserIdentityFromProvider(c, oauthProvider, token)
	if err != nil {
		response.BadRequest(c, "OAUTH_ERROR", "Failed to get user identity: "+err.Error())
		return
	}

	// Extract username (use email prefix as fallback)
	username := email
	if idx := strings.Index(email, "@"); idx > 0 {
		username = email[:idx]
	}

	// If the browser already has a valid session, link this OAuth identity to that
	// existing account and keep the current session, instead of creating (and
	// switching to) a separate user. This prevents an admin who connects GitHub
	// during App setup from being silently downgraded to a fresh non-admin account.
	if current := h.currentSessionUser(c); current != nil {
		if linkErr := h.oauthService.LinkOAuthAccount(c, current.ID, oauthProvider, subject); linkErr == nil {
			h.maybeMirrorGitHubRepos(oauthProvider, current.ID, token)
			h.finishOAuthRedirect(c, gin.H{"user": current}, "GitHub account linked")
			return
		}
		// Linking failed (e.g. the identity is already bound to a different user);
		// fall through to the normal login path below.
	}

	// Create or update user from OAuth data
	user, err := h.oauthService.CreateOrUpdateUserFromOAuth(c, oauthProvider, subject, email, username)
	if err != nil {
		logServerError(c, err)
		response.InternalError(c, "Failed to create or update user")
		return
	}

	// Create session
	sessionToken, err := h.authService.GenerateSessionToken()
	if err != nil {
		logServerError(c, err)
		response.InternalError(c, "Failed to generate session token")
		return
	}
	expiresAt := time.Now().UTC().Add(h.authService.SessionDuration())

	// Store only the SHA-256 hash of the token at rest (the raw token goes to the
	// cookie), matching the password-login path and the hashed session lookup.
	session := &models.Session{
		Token:  crypto.HashToken(sessionToken),
		UserID: user.ID,
		Data:   `{"auth_type":"oauth","provider":"` + string(oauthProvider) + `"}`,
		Expiry: expiresAt,
	}
	if err := h.authService.DB().Create(session).Error; err != nil {
		logServerError(c, err)
		response.InternalError(c, "Failed to create session")
		return
	}

	// Set session cookie (HttpOnly, SameSite=Lax, Secure over HTTPS). Match the
	// cookie lifetime to the server-side session expiry instead of a hardcoded
	// 24h, so the two cannot diverge when SessionDuration() is not 24h.
	maxAge := int(time.Until(expiresAt).Seconds())
	if maxAge < 0 {
		maxAge = 0
	}
	setSessionCookie(c, sessionToken, maxAge)

	// Kick off a background import of all the user's GitHub repositories.
	h.maybeMirrorGitHubRepos(oauthProvider, user.ID, token)

	// Redirect to frontend or return success. A redirect target may come from the
	// query string or from the oauth_redirect cookie set at login time.
	h.finishOAuthRedirect(c, gin.H{
		"user":          user,
		"session_token": sessionToken,
		"expires_at":    expiresAt,
	}, "Login successful")
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
	c.SetCookie("oauth_link_state", state, 3600, "/", "", requestIsHTTPS(c), true)

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
	c.SetCookie("oauth_link_state", "", -1, "/", "", requestIsHTTPS(c), true)

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
	// Bound the token exchange with an explicit timeout so a slow or hung provider
	// cannot block the request for its full lifetime.
	exCtx, exCancel := context.WithTimeout(c.Request.Context(), oauthExchangeTimeout)
	defer exCancel()
	token, err := oauthConfig.Exchange(exCtx, code)
	if err != nil {
		response.BadRequest(c, "OAUTH_ERROR", "Failed to exchange code for token: "+err.Error())
		return
	}

	// Resolve the stable provider subject (not the rotating access token) to link.
	_, subject, err := h.oauthService.GetUserIdentityFromProvider(c, oauthProvider, token)
	if err != nil {
		response.BadRequest(c, "OAUTH_ERROR", "Failed to get user identity: "+err.Error())
		return
	}

	// Link OAuth account to existing user
	if err := h.oauthService.LinkOAuthAccount(c, user.ID, oauthProvider, subject); err != nil {
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

	// Unlink OAuth account. Load a fresh copy of the user row and clear the fields
	// on that copy — never mutate the shared, cache-owned *models.User returned by
	// GetCurrentUser (the auth cache documents its cached user as never mutated).
	var fresh models.User
	if err := h.authService.DB().First(&fresh, user.ID).Error; err != nil {
		logServerError(c, err)
		response.InternalError(c, "Failed to unlink OAuth account")
		return
	}
	fresh.OAuthProvider = nil
	fresh.OAuthSubject = nil
	if err := h.authService.DB().Save(&fresh).Error; err != nil {
		logServerError(c, err)
		response.InternalError(c, "Failed to unlink OAuth account")
		return
	}

	// Evict cached credentials so the updated user (without the OAuth link) is
	// re-read rather than served stale from the auth cache for the remainder of its
	// TTL.
	h.authService.EvictUserCache(user.ID)

	response.SuccessWithMessage(c, "OAuth account unlinked successfully", nil)
}

// ManifestSetup handles POST /api/v1/oauth/github/manifest-setup (admin only).
// It issues a one-time setup nonce, stored in an httpOnly cookie, that the
// browser passes to GitHub as the manifest "state". ManifestCallback validates
// it so an attacker cannot drive an authenticated admin through the callback
// with an attacker-controlled manifest code.
func (h *OAuthHandler) ManifestSetup(c *gin.Context) {
	user, ok := middleware.GetCurrentUser(c)
	if !ok || !user.IsAdmin() {
		response.Unauthorized(c, "Admin access required")
		return
	}

	nonce := uuid.New().String()
	c.SetCookie("github_setup_state", nonce, 600, "/", "", requestIsHTTPS(c), true)
	response.Success(c, gin.H{"state": nonce})
}

// ManifestCallback handles GET /api/v1/oauth/github/manifest-callback.
// GitHub redirects here with a ?code=CODE parameter after manifest creation.
func (h *OAuthHandler) ManifestCallback(c *gin.Context) {
	// This callback persists GitHub App credentials, so it must only be usable
	// by an authenticated admin who initiated the setup. The setup is started
	// from the admin-only /config page, and the admin's session cookie is sent
	// on this top-level redirect back from GitHub.
	sessionCookie, cookieErr := c.Cookie("session")
	if cookieErr != nil || sessionCookie == "" {
		c.Redirect(http.StatusFound, "/config?error=unauthorized_setup")
		return
	}
	user, sessErr := h.authService.ValidateSession(c.Request.Context(), sessionCookie)
	if sessErr != nil || user == nil || !user.IsAdmin() {
		c.Redirect(http.StatusFound, "/config?error=unauthorized_setup")
		return
	}

	// Validate the one-time setup nonce issued by ManifestSetup when the admin
	// started the flow, then consume it. This blocks CSRF-driven callbacks that
	// would otherwise persist attacker-supplied GitHub App credentials.
	stateCookie, stateErr := c.Cookie("github_setup_state")
	c.SetCookie("github_setup_state", "", -1, "/", "", requestIsHTTPS(c), true)
	state := c.Query("state")
	if stateErr != nil || stateCookie == "" || state == "" || state != stateCookie {
		c.Redirect(http.StatusFound, "/config?error=invalid_setup_state")
		return
	}

	code := c.Query("code")
	if code == "" {
		response.BadRequest(c, "INVALID_REQUEST", "Missing code parameter")
		return
	}

	// 1. Exchange manifest code for credentials. Bound the exchange with an
	// explicit timeout so a slow/hung GitHub response cannot block the request.
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()

	apiURL := fmt.Sprintf("https://api.github.com/app-manifests/%s/conversions", code)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, nil)
	if err != nil {
		c.Redirect(http.StatusFound, "/config?error=failed_to_create_exchange_request")
		return
	}

	// GitHub requires User-Agent header for all API calls
	req.Header.Set("User-Agent", "GitDuppy")
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	// Use a client with an explicit timeout rather than http.DefaultClient (item
	// 27); the request context already carries a 30s deadline, and this matches it
	// so a hung connection cannot outlive it.
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
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

	// 3. Credentials are stored; immediately start the GitHub OAuth login flow so
	// the user is authenticated right after registering the App (single click flow).
	c.Redirect(http.StatusFound, "/api/v1/oauth/github/login?setup=1")
}
