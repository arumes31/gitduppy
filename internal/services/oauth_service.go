package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/github"
	"golang.org/x/oauth2/gitlab"
	"golang.org/x/oauth2/google"

	"github.com/gitduppy/gitduppy/internal/config"
	"github.com/gitduppy/gitduppy/internal/database"
	"github.com/gitduppy/gitduppy/internal/models"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// OAuthService handles OAuth2/OIDC authentication.
type OAuthService struct {
	db     *gorm.DB
	config *config.Config
}

// NewOAuthService creates a new OAuth service.
func NewOAuthService(cfg *config.Config) *OAuthService {
	return &OAuthService{
		db:     database.GetDB(),
		config: cfg,
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
func (s *OAuthService) GetOAuthConfig(provider OAuthProvider) (*oauth2.Config, error) {
	switch provider {
	case GitHubProvider:
		if s.config.OAuth.GitHub.ClientID == "" || s.config.OAuth.GitHub.ClientSecret == "" {
			return nil, errors.New("github oauth not configured")
		}
		return &oauth2.Config{
			ClientID:     s.config.OAuth.GitHub.ClientID,
			ClientSecret: s.config.OAuth.GitHub.ClientSecret,
			RedirectURL:  s.config.OAuth.GitHub.RedirectURL,
			Scopes:       s.config.OAuth.GitHub.Scopes,
			Endpoint:     github.Endpoint,
		}, nil
	case GitLabProvider:
		if s.config.OAuth.GitLab.ClientID == "" || s.config.OAuth.GitLab.ClientSecret == "" {
			return nil, errors.New("gitlab oauth not configured")
		}
		return &oauth2.Config{
			ClientID:     s.config.OAuth.GitLab.ClientID,
			ClientSecret: s.config.OAuth.GitLab.ClientSecret,
			RedirectURL:  s.config.OAuth.GitLab.RedirectURL,
			Scopes:       s.config.OAuth.GitLab.Scopes,
			Endpoint:     gitlab.Endpoint,
		}, nil
	case GoogleProvider:
		if s.config.OAuth.Google.ClientID == "" || s.config.OAuth.Google.ClientSecret == "" {
			return nil, errors.New("google oauth not configured")
		}
		return &oauth2.Config{
			ClientID:     s.config.OAuth.Google.ClientID,
			ClientSecret: s.config.OAuth.Google.ClientSecret,
			RedirectURL:  s.config.OAuth.Google.RedirectURL,
			Scopes:       s.config.OAuth.Google.Scopes,
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
}

// GetUserEmailFromProvider extracts email from OAuth provider response.
func (s *OAuthService) GetUserEmailFromProvider(ctx context.Context, provider OAuthProvider, token *oauth2.Token) (string, error) {
	httpClient := s.getHTTPClient(ctx, token)

	switch provider {
	case GitHubProvider:
		return s.getGitHubEmail(ctx, httpClient)
	case GitLabProvider:
		return s.getGitLabEmail(ctx, httpClient)
	case GoogleProvider:
		return s.getGoogleEmail(ctx, token)
	default:
		return "", fmt.Errorf("unsupported oauth provider: %s", provider)
	}
}

// getGitHubEmail fetches the user's email from GitHub API.
func (s *OAuthService) getGitHubEmail(ctx context.Context, httpClient *http.Client) (string, error) {
	// Try to get user first.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/user", nil)
	if err != nil {
		return "", err
	}
	userResp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to get github user: %w", err)
	}
	defer userResp.Body.Close()

	var user GitHubUser
	if decodeErr := json.NewDecoder(userResp.Body).Decode(&user); decodeErr != nil {
		return "", fmt.Errorf("failed to decode github user: %w", decodeErr)
	}

	if user.Email != nil && *user.Email != "" {
		return *user.Email, nil
	}

	// Try to get email from emails endpoint.
	emailReq, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/user/emails", nil)
	if err != nil {
		return "", err
	}
	emailsResp, err := httpClient.Do(emailReq)
	if err != nil {
		return "", fmt.Errorf("failed to get github emails: %w", err)
	}
	defer emailsResp.Body.Close()

	var emails []GitHubEmail
	if decodeErr := json.NewDecoder(emailsResp.Body).Decode(&emails); decodeErr != nil {
		return "", fmt.Errorf("failed to decode github emails: %w", decodeErr)
	}

	for _, email := range emails {
		if email.Primary && email.Verified {
			return email.Email, nil
		}
	}

	return "", errors.New("no verified primary email found")
}

// getGitLabEmail fetches the user's email from GitLab API.
func (s *OAuthService) getGitLabEmail(ctx context.Context, httpClient *http.Client) (string, error) {
	baseURL := "https://gitlab.com"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/api/v4/user", baseURL), nil)
	if err != nil {
		return "", err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to get gitlab user: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("gitlab API returned status: %d", resp.StatusCode)
	}

	var user GitLabUser
	if decodeErr := json.NewDecoder(resp.Body).Decode(&user); decodeErr != nil {
		return "", fmt.Errorf("failed to decode gitlab user: %w", decodeErr)
	}

	if user.Email == "" {
		return "", errors.New("no email found in gitlab response")
	}

	return user.Email, nil
}

// getGoogleEmail extracts email from Google OIDC token.
func (s *OAuthService) getGoogleEmail(ctx context.Context, token *oauth2.Token) (string, error) {
	provider, err := oidc.NewProvider(ctx, "https://accounts.google.com")
	if err != nil {
		return "", fmt.Errorf("failed to create oidc provider: %w", err)
	}

	idToken, err := provider.Verifier(&oidc.Config{ClientID: s.config.OAuth.Google.ClientID}).Verify(ctx, token.AccessToken)
	if err != nil {
		return "", fmt.Errorf("failed to verify id token: %w", err)
	}

	var claims map[string]interface{}
	if err := idToken.Claims(&claims); err != nil {
		return "", fmt.Errorf("failed to extract claims: %w", err)
	}

	email, ok := claims["email"].(string)
	if !ok {
		return "", errors.New("email not found in token claims")
	}

	return email, nil
}

// getHTTPClient returns an HTTP client with the given token.
func (s *OAuthService) getHTTPClient(ctx context.Context, token *oauth2.Token) *http.Client {
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
