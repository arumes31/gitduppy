package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/github"
	"golang.org/x/oauth2/gitlab"
	"golang.org/x/oauth2/google"

	"github.com/gitduppy/gitduppy/internal/database"
	"github.com/gitduppy/gitduppy/internal/models"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// oauthHTTPTimeout bounds outbound OAuth provider HTTP calls (user/email/repo
// APIs and token exchanges) so a slow or hung provider cannot pin a request.
const oauthHTTPTimeout = 15 * time.Second

// OAuthService handles OAuth2/OIDC authentication.
type OAuthService struct {
	db            *gorm.DB
	configService *ConfigService
	// repoService is used to import the authenticated user's GitHub repositories
	// as mirrors. Wired via SetRepositoryService to avoid a constructor cycle.
	repoService *RepositoryService

	// googleProvider caches the Google OIDC provider so its discovery-document
	// HTTP fetch happens once rather than on every login.
	googleMu       sync.Mutex
	googleProvider *oidc.Provider
}

// SetRepositoryService wires the repository service used to mirror GitHub repos.
func (s *OAuthService) SetRepositoryService(rs *RepositoryService) {
	s.repoService = rs
}

// GitHubRepo is the subset of the GitHub repositories API response we need.
type GitHubRepo struct {
	Name          string `json:"name"`
	CloneURL      string `json:"clone_url"`
	DefaultBranch string `json:"default_branch"`
	Private       bool   `json:"private"`
	Archived      bool   `json:"archived"`
}

// listGitHubRepos returns every repository owned by the authenticated user,
// following pagination (capped to avoid an unbounded loop).
func (s *OAuthService) listGitHubRepos(ctx context.Context, httpClient *http.Client) ([]GitHubRepo, error) {
	var all []GitHubRepo
	for page := 1; page <= 50; page++ {
		url := fmt.Sprintf("https://api.github.com/user/repos?per_page=100&affiliation=owner&page=%d", page)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		resp, err := httpClient.Do(req)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			resp.Body.Close()
			return nil, fmt.Errorf("github repos request returned status %d", resp.StatusCode)
		}
		var batch []GitHubRepo
		decodeErr := json.NewDecoder(resp.Body).Decode(&batch)
		resp.Body.Close()
		if decodeErr != nil {
			return nil, fmt.Errorf("failed to decode github repos: %w", decodeErr)
		}
		all = append(all, batch...)
		if len(batch) < 100 {
			break
		}
	}
	return all, nil
}

// MirrorAllGitHubRepos imports every repository owned by the authenticated GitHub
// user as a mirror for userID (creating new ones, refreshing credentials on
// existing ones). It is best-effort: a per-repo failure is skipped so one bad
// repo cannot abort the batch. Returns the number of newly created mirrors.
func (s *OAuthService) MirrorAllGitHubRepos(ctx context.Context, userID uuid.UUID, token *oauth2.Token) (int, error) {
	if s.repoService == nil {
		return 0, errors.New("repository service not configured")
	}
	httpClient := s.getHTTPClient(ctx, token)
	repos, err := s.listGitHubRepos(ctx, httpClient)
	if err != nil {
		return 0, err
	}
	created := 0
	for _, r := range repos {
		if r.Archived || r.CloneURL == "" {
			continue
		}
		isNew, upErr := s.repoService.UpsertGitHubMirror(ctx, userID, r.Name, r.CloneURL, r.DefaultBranch, r.Private, token.AccessToken)
		if upErr != nil {
			continue
		}
		if isNew {
			created++
		}
	}
	return created, nil
}

// googleOIDCProvider returns a cached Google OIDC provider, performing the
// discovery-document fetch only once (cached on first success, retried on
// failure so a transient network error is not remembered forever).
func (s *OAuthService) googleOIDCProvider(ctx context.Context) (*oidc.Provider, error) {
	s.googleMu.Lock()
	defer s.googleMu.Unlock()
	if s.googleProvider != nil {
		return s.googleProvider, nil
	}
	p, err := oidc.NewProvider(ctx, "https://accounts.google.com")
	if err != nil {
		return nil, err
	}
	s.googleProvider = p
	return p, nil
}

// NewOAuthService creates a new OAuth service.
func NewOAuthService(configService *ConfigService) *OAuthService {
	return &OAuthService{
		db:            database.GetDB(),
		configService: configService,
	}
}

// OAuthProvider represents an OAuth provider.
type OAuthProvider string

const (
	GitHubProvider OAuthProvider = "github"
	GitLabProvider OAuthProvider = "gitlab"
	GoogleProvider OAuthProvider = "google"
)

// GetOAuthConfig returns the OAuth2 config for a provider.
func (s *OAuthService) GetOAuthConfig(ctx context.Context, provider OAuthProvider) (*oauth2.Config, error) {
	switch provider {
	case GitHubProvider:
		ghConfig := s.configService.GetGitHubOAuth(ctx)
		if ghConfig.ClientID == "" || ghConfig.ClientSecret == "" {
			return nil, errors.New("github oauth not configured")
		}
		return &oauth2.Config{
			ClientID:     ghConfig.ClientID,
			ClientSecret: ghConfig.ClientSecret,
			RedirectURL:  ghConfig.RedirectURL,
			Scopes:       ghConfig.Scopes,
			Endpoint:     github.Endpoint,
		}, nil
	case GitLabProvider:
		glConfig := s.configService.GetGitLabOAuth(ctx)
		if glConfig.ClientID == "" || glConfig.ClientSecret == "" {
			return nil, errors.New("gitlab oauth not configured")
		}
		return &oauth2.Config{
			ClientID:     glConfig.ClientID,
			ClientSecret: glConfig.ClientSecret,
			RedirectURL:  glConfig.RedirectURL,
			Scopes:       glConfig.Scopes,
			Endpoint:     gitlab.Endpoint,
		}, nil
	case GoogleProvider:
		ggConfig := s.configService.GetGoogleOAuth(ctx)
		if ggConfig.ClientID == "" || ggConfig.ClientSecret == "" {
			return nil, errors.New("google oauth not configured")
		}
		return &oauth2.Config{
			ClientID:     ggConfig.ClientID,
			ClientSecret: ggConfig.ClientSecret,
			RedirectURL:  ggConfig.RedirectURL,
			Scopes:       ggConfig.Scopes,
			Endpoint:     google.Endpoint,
		}, nil
	default:
		return nil, fmt.Errorf("unsupported oauth provider: %s", provider)
	}
}

// GitHubUser represents a GitHub user API response.
type GitHubUser struct {
	Email *string `json:"email"`
	Login string  `json:"login"`
	ID    int64   `json:"id"`
}

// GitHubEmail represents a GitHub email API response.
type GitHubEmail struct {
	Email    string `json:"email"`
	Primary  bool   `json:"primary"`
	Verified bool   `json:"verified"`
}

// GitLabUser represents a GitLab user API response.
type GitLabUser struct {
	Email string `json:"email"`
	ID    int64  `json:"id"`
}

// GetUserIdentityFromProvider extracts the email and a stable provider-specific
// subject (account id / sub claim) from an OAuth provider response. The subject
// — not the rotating access token — is what identifies the account across logins.
func (s *OAuthService) GetUserIdentityFromProvider(ctx context.Context, provider OAuthProvider, token *oauth2.Token) (email, subject string, err error) {
	httpClient := s.getHTTPClient(ctx, token)

	switch provider {
	case GitHubProvider:
		return s.getGitHubEmail(ctx, httpClient)
	case GitLabProvider:
		return s.getGitLabEmail(ctx, httpClient)
	case GoogleProvider:
		return s.getGoogleEmail(ctx, token)
	default:
		return "", "", fmt.Errorf("unsupported oauth provider: %s", provider)
	}
}

// getGitHubEmail fetches the user's email and stable subject from the GitHub API.
func (s *OAuthService) getGitHubEmail(ctx context.Context, httpClient *http.Client) (string, string, error) {
	// Try to get user first.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/user", nil)
	if err != nil {
		return "", "", err
	}
	userResp, err := httpClient.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("failed to get github user: %w", err)
	}
	defer userResp.Body.Close()

	// Guard on the status code before decoding: a token without the required
	// permission gets a JSON error *object*, which would otherwise fail to
	// unmarshal into the user struct with a confusing error.
	if userResp.StatusCode < 200 || userResp.StatusCode >= 300 {
		return "", "", fmt.Errorf("github user request returned status %d", userResp.StatusCode)
	}

	var user GitHubUser
	if decodeErr := json.NewDecoder(userResp.Body).Decode(&user); decodeErr != nil {
		return "", "", fmt.Errorf("failed to decode github user: %w", decodeErr)
	}

	// The numeric account ID is the only stable identity. The login is mutable
	// (users can rename), so it must never be used as the subject — require a real
	// numeric id.
	if user.ID == 0 {
		return "", "", errors.New("github user has no stable numeric identifier")
	}
	subject := fmt.Sprintf("%d", user.ID)

	// Prefer a public primary email when the account exposes one.
	if user.Email != nil && *user.Email != "" {
		return *user.Email, subject, nil
	}

	// Otherwise try the emails endpoint (needs the user email permission/scope).
	if email, ok := s.githubPrimaryEmail(ctx, httpClient); ok {
		return email, subject, nil
	}

	// The email is private and unreadable with this token (e.g. a GitHub App
	// without the email permission). Fall back to GitHub's stable no-reply address
	// derived from the account id and login so login still succeeds. It is unique
	// per account and cannot collide with a real local user's email.
	if user.Login == "" {
		return fmt.Sprintf("%d@users.noreply.github.com", user.ID), subject, nil
	}
	return fmt.Sprintf("%d+%s@users.noreply.github.com", user.ID, user.Login), subject, nil
}

// githubPrimaryEmail returns the verified primary email from the GitHub emails
// endpoint, or ok=false when it is unavailable (missing permission, a non-2xx
// response, an unexpected body, or no verified primary email). It never returns
// an error so callers can fall back gracefully.
func (s *OAuthService) githubPrimaryEmail(ctx context.Context, httpClient *http.Client) (string, bool) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/user/emails", nil)
	if err != nil {
		return "", false
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", false
	}
	defer resp.Body.Close()

	// A token lacking the email permission returns a JSON error object (not an
	// array); only decode when the request actually succeeded.
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", false
	}

	var emails []GitHubEmail
	if decodeErr := json.NewDecoder(resp.Body).Decode(&emails); decodeErr != nil {
		return "", false
	}
	for _, e := range emails {
		if e.Primary && e.Verified {
			return e.Email, true
		}
	}
	return "", false
}

// getGitLabEmail fetches the user's email and stable subject from the GitLab API.
func (s *OAuthService) getGitLabEmail(ctx context.Context, httpClient *http.Client) (string, string, error) {
	baseURL := "https://gitlab.com"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/api/v4/user", baseURL), nil)
	if err != nil {
		return "", "", err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("failed to get gitlab user: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", "", fmt.Errorf("gitlab API returned status: %d", resp.StatusCode)
	}

	var user GitLabUser
	if decodeErr := json.NewDecoder(resp.Body).Decode(&user); decodeErr != nil {
		return "", "", fmt.Errorf("failed to decode gitlab user: %w", decodeErr)
	}

	if user.Email == "" {
		return "", "", errors.New("no email found in gitlab response")
	}
	if user.ID == 0 {
		return "", "", errors.New("gitlab user has no stable identifier")
	}

	return user.Email, fmt.Sprintf("%d", user.ID), nil
}

// getGoogleEmail extracts the email and stable subject (sub claim) from a Google
// OIDC token.
func (s *OAuthService) getGoogleEmail(ctx context.Context, token *oauth2.Token) (string, string, error) {
	provider, err := s.googleOIDCProvider(ctx)
	if err != nil {
		return "", "", fmt.Errorf("failed to create oidc provider: %w", err)
	}

	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok || rawIDToken == "" {
		return "", "", errors.New("no id_token found in oauth token response")
	}

	ggConfig := s.configService.GetGoogleOAuth(ctx)
	idToken, err := provider.Verifier(&oidc.Config{ClientID: ggConfig.ClientID}).Verify(ctx, rawIDToken)
	if err != nil {
		return "", "", fmt.Errorf("failed to verify id token: %w", err)
	}

	var claims map[string]any
	if err := idToken.Claims(&claims); err != nil {
		return "", "", fmt.Errorf("failed to extract claims: %w", err)
	}

	email, ok := claims["email"].(string)
	if !ok {
		return "", "", errors.New("email not found in token claims")
	}
	// Only trust a verified email. An unverified Google email must not be able to
	// create or link an account (someone could otherwise claim an address they do
	// not control).
	if verified, _ := claims["email_verified"].(bool); !verified {
		return "", "", errors.New("google email is not verified")
	}
	subject, _ := claims["sub"].(string)
	if subject == "" {
		return "", "", errors.New("sub not found in token claims")
	}

	return email, subject, nil
}

// getHTTPClient returns an HTTP client that authenticates with the given token.
// The base client carries an explicit timeout (propagated onto the oauth2 client)
// so outbound provider API calls (GitHub/GitLab user + repo listing) cannot hang
// without bound when a request context has no deadline of its own.
func (s *OAuthService) getHTTPClient(ctx context.Context, token *oauth2.Token) *http.Client {
	ctx = context.WithValue(ctx, oauth2.HTTPClient, &http.Client{Timeout: oauthHTTPTimeout})
	return oauth2.NewClient(ctx, oauth2.StaticTokenSource(token))
}

// LinkOAuthAccount links an OAuth account to an existing user.
func (s *OAuthService) LinkOAuthAccount(_ context.Context, userID uuid.UUID, provider OAuthProvider, subject string) error {
	var user models.User
	if err := s.db.First(&user, userID).Error; err != nil {
		return errors.New("user not found")
	}

	// Check if this OAuth account is already linked to another user
	var existingUser models.User
	if err := s.db.Where("oauth_provider = ? AND oauth_subject = ?", string(provider), subject).First(&existingUser).Error; err == nil {
		if existingUser.ID != userID {
			return errors.New("oauth account already linked to another user")
		}
		// Already linked to this user, nothing to do
		return nil
	}

	// Link the OAuth account
	user.OAuthProvider = stringPtr(string(provider))
	user.OAuthSubject = stringPtr(subject)
	return s.db.Save(&user).Error
}

// CreateOrUpdateUserFromOAuth creates or updates a user from OAuth data.
func (s *OAuthService) CreateOrUpdateUserFromOAuth(_ context.Context, provider OAuthProvider, subject string, email string, username string) (*models.User, error) {
	// First, try to find existing user by OAuth provider and subject
	var existingUser models.User
	if err := s.db.Where("oauth_provider = ? AND oauth_subject = ?", string(provider), subject).First(&existingUser).Error; err == nil {
		// Update email if it changed
		if existingUser.Email != email {
			existingUser.Email = email
			s.db.Save(&existingUser)
		}
		return &existingUser, nil
	}

	// Try to find existing user by email
	var userByEmail models.User
	if err := s.db.Where("email = ?", email).First(&userByEmail).Error; err == nil {
		// Link OAuth to existing user
		userByEmail.OAuthProvider = stringPtr(string(provider))
		userByEmail.OAuthSubject = stringPtr(subject)
		if err := s.db.Save(&userByEmail).Error; err != nil {
			return nil, err
		}
		return &userByEmail, nil
	}

	// Create new user
	newUser := &models.User{
		ID:            uuid.New(),
		Username:      username,
		Email:         email,
		Role:          "user",
		IsActive:      true,
		OAuthProvider: stringPtr(string(provider)),
		OAuthSubject:  stringPtr(subject),
	}
	if err := s.db.Create(newUser).Error; err != nil {
		return nil, err
	}
	return newUser, nil
}

// stringPtr returns a pointer to a string.
func stringPtr(s string) *string {
	return &s
}

// SaveGitHubCredentials saves the GitHub OAuth client credentials in system settings.
func (s *OAuthService) SaveGitHubCredentials(ctx context.Context, clientID, clientSecret string) error {
	idKey := "oauth2_github_client_id"
	// #nosec G101 - This is a settings key name string, not a hardcoded secret credential
	secretKey := "oauth2_github_client_secret"

	// Persist the client ID and secret together so a failure on either write
	// cannot leave the credential pair half-updated.
	writes := []SettingWrite{
		{Key: idKey, Value: clientID, Description: "OAuth Client ID for github", Encrypt: false},
	}
	if clientSecret != "" {
		writes = append(writes, SettingWrite{Key: secretKey, Value: clientSecret, Description: "OAuth Client Secret for github", Encrypt: true})
	}
	return s.configService.SetSettings(ctx, writes...)
}
