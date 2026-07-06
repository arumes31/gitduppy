package services

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/gitduppy/gitduppy/internal/database"
	"github.com/gitduppy/gitduppy/internal/models"
	"github.com/gitduppy/gitduppy/pkg/crypto"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// AuthService handles authentication logic.
type AuthService struct {
	db              *gorm.DB
	passwordService *crypto.PasswordService
	sessionDuration time.Duration
}

// NewAuthService creates a new auth service.
func NewAuthService(sessionDuration time.Duration) *AuthService {
	return &AuthService{
		db:              database.GetDB(),
		passwordService: crypto.NewPasswordService(),
		sessionDuration: sessionDuration,
	}
}

// LoginRequest represents a login request.
type LoginRequest struct {
	Username   string `json:"username" validate:"required"`
	Password   string `json:"password" validate:"required"`
	RememberMe bool   `json:"remember_me"`
}

// LoginResponse represents a login response.
type LoginResponse struct {
	User         *models.User `json:"user"`
	SessionToken string       `json:"session_token,omitempty"`
	ExpiresAt    time.Time    `json:"expires_at"`
}

// Login authenticates a user and creates a session.
func (s *AuthService) Login(_ context.Context, req *LoginRequest) (*LoginResponse, error) {
	// Find the user with a deterministic lookup: an exact username match takes
	// precedence, falling back to an email match only when no username matches.
	// A combined "username = ? OR email = ?" query could return whichever row
	// the database happened to order first and authenticate the wrong account
	// when one user's username equals another user's email.
	var user models.User
	err := s.db.Where("username = ?", req.Username).First(&user).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		err = s.db.Where("email = ?", req.Username).First(&user).Error
	}
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("invalid username or password")
		}
		return nil, err
	}

	// Check if user is active
	if !user.IsActive {
		return nil, errors.New("account is disabled")
	}

	// Verify password
	if user.PasswordHash == nil || !s.passwordService.Verify(*user.PasswordHash, req.Password) {
		return nil, errors.New("invalid username or password")
	}

	// Update last login
	now := time.Now()
	user.LastLogin = &now
	s.db.Save(&user)

	// Generate session token
	sessionToken, err := s.GenerateSessionToken()
	if err != nil {
		return nil, fmt.Errorf("failed to generate session token: %w", err)
	}
	expiresAt := time.Now().Add(s.sessionDuration)

	// Create session
	session := &models.Session{
		Token:  sessionToken,
		UserID: user.ID,
		Data:   `{"auth_type":"password"}`,
		Expiry: expiresAt,
	}
	if err := s.db.Create(session).Error; err != nil {
		return nil, err
	}

	return &LoginResponse{
		User:         &user,
		SessionToken: sessionToken,
		ExpiresAt:    expiresAt,
	}, nil
}

// Logout invalidates a session.
func (s *AuthService) Logout(_ context.Context, sessionToken string) error {
	return s.db.Where("token = ?", sessionToken).Delete(&models.Session{}).Error
}

// RefreshSession extends a session's expiry.
func (s *AuthService) RefreshSession(_ context.Context, sessionToken string) (*models.Session, error) {
	var session models.Session
	if err := s.db.Where("token = ? AND expiry > ?", sessionToken, time.Now()).First(&session).Error; err != nil {
		return nil, errors.New("invalid or expired session")
	}

	// Extend expiry
	session.Expiry = time.Now().Add(s.sessionDuration)
	if err := s.db.Save(&session).Error; err != nil {
		return nil, err
	}

	return &session, nil
}

// ValidateSession checks if a session is valid and returns the user.
func (s *AuthService) ValidateSession(_ context.Context, sessionToken string) (*models.User, error) {
	var session models.Session
	if err := s.db.Where("token = ? AND expiry > ?", sessionToken, time.Now()).First(&session).Error; err != nil {
		return nil, errors.New("invalid or expired session")
	}

	var user models.User
	if err := s.db.First(&user, session.UserID).Error; err != nil {
		return nil, err
	}

	if !user.IsActive {
		return nil, errors.New("account is disabled")
	}

	return &user, nil
}

// GenerateSessionToken creates a random session token.
func (s *AuthService) GenerateSessionToken() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(bytes), nil
}

// SessionDuration returns the configured session duration.
func (s *AuthService) SessionDuration() time.Duration {
	return s.sessionDuration
}

// DB returns the database connection.
func (s *AuthService) DB() *gorm.DB {
	return s.db
}

// ChangePassword changes a user's password.
func (s *AuthService) ChangePassword(_ context.Context, userID uuid.UUID, oldPassword, newPassword string) error {
	var user models.User
	if err := s.db.First(&user, userID).Error; err != nil {
		return errors.New("user not found")
	}

	// Verify old password
	if user.PasswordHash == nil || !s.passwordService.Verify(*user.PasswordHash, oldPassword) {
		return errors.New("invalid current password")
	}

	// Hash new password
	newHash, err := s.passwordService.Hash(newPassword)
	if err != nil {
		return err
	}

	// Persist the new password and invalidate every existing session for this
	// user in one transaction. A password change must log out all devices —
	// including any attacker holding a stolen session cookie — otherwise the old
	// sessions stay valid until natural expiry.
	user.PasswordHash = &newHash
	return s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Save(&user).Error; err != nil {
			return err
		}
		return tx.Where("user_id = ?", userID).Delete(&models.Session{}).Error
	})
}

// GetUserByID retrieves a user by ID.
func (s *AuthService) GetUserByID(_ context.Context, id uuid.UUID) (*models.User, error) {
	var user models.User
	if err := s.db.First(&user, id).Error; err != nil {
		return nil, err
	}
	return &user, nil
}
