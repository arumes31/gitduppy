package services

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"time"

	"github.com/gitduppy/gitduppy/internal/database"
	"github.com/gitduppy/gitduppy/internal/models"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// APIKeyService handles API key CRUD operations.
type APIKeyService struct {
	db *gorm.DB
}

// NewAPIKeyService creates a new API key service.
func NewAPIKeyService() *APIKeyService {
	return &APIKeyService{
		db: database.GetDB(),
	}
}

// CreateAPIKeyRequest represents a create API key request.
type CreateAPIKeyRequest struct {
	Name          string `json:"name" validate:"required"`
	ExpiresInDays int    `json:"expires_in_days"`
}

// CreateAPIKeyResponse represents the response when creating an API key.
type CreateAPIKeyResponse struct {
	ID        uuid.UUID  `json:"id"`
	Name      string     `json:"name"`
	Key       string     `json:"key"`
	KeyPrefix string     `json:"key_prefix"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
}

// CreateAPIKey creates a new API key.
func (s *APIKeyService) CreateAPIKey(_ context.Context, userID uuid.UUID, req *CreateAPIKeyRequest) (*CreateAPIKeyResponse, error) {
	// Generate random key
	keyBytes := make([]byte, 32)
	if _, err := rand.Read(keyBytes); err != nil {
		return nil, err
	}
	key := "gm_" + base64.RawURLEncoding.EncodeToString(keyBytes)

	// Hash the key for storage
	hash := sha256.Sum256([]byte(key))
	keyHash := hex.EncodeToString(hash[:])
	keyPrefix := key[:min(8, len(key))]

	var expiresAt *time.Time
	if req.ExpiresInDays > 0 {
		exp := time.Now().Add(time.Duration(req.ExpiresInDays) * 24 * time.Hour)
		expiresAt = &exp
	}

	apiKey := &models.APIKey{
		ID:        uuid.New(),
		UserID:    userID,
		KeyHash:   keyHash,
		KeyPrefix: keyPrefix,
		Name:      req.Name,
		IsActive:  true,
		ExpiresAt: expiresAt,
		CreatedAt: time.Now(),
	}

	if err := s.db.Create(apiKey).Error; err != nil {
		return nil, err
	}

	return &CreateAPIKeyResponse{
		ID:        apiKey.ID,
		Name:      apiKey.Name,
		Key:       key,
		KeyPrefix: apiKey.KeyPrefix,
		ExpiresAt: expiresAt,
		CreatedAt: apiKey.CreatedAt,
	}, nil
}

// ListAPIKeys returns all API keys for a user.
func (s *APIKeyService) ListAPIKeys(_ context.Context, userID uuid.UUID) ([]models.APIKey, error) {
	var keys []models.APIKey
	err := s.db.Where("user_id = ?", userID).Order("created_at DESC").Find(&keys).Error
	return keys, err
}

// GetAPIKeyByID retrieves an API key by ID.
func (s *APIKeyService) GetAPIKeyByID(_ context.Context, id uuid.UUID) (*models.APIKey, error) {
	var key models.APIKey
	if err := s.db.First(&key, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("API key not found")
		}
		return nil, err
	}
	return &key, nil
}

// RevokeAPIKey revokes an API key.
func (s *APIKeyService) RevokeAPIKey(_ context.Context, id uuid.UUID) error {
	result := s.db.Model(&models.APIKey{}).Where("id = ?", id).Update("is_active", false)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return errors.New("API key not found")
	}
	return nil
}

// ValidateAPIKey validates an API key and returns the associated user.
func (s *APIKeyService) ValidateAPIKey(key string) (*models.User, error) {
	// Hash the provided key
	hash := sha256.Sum256([]byte(key))
	keyHash := hex.EncodeToString(hash[:])

	// Find the API key in database
	var apiKey models.APIKey
	if err := s.db.Where("key_hash = ? AND is_active = true", keyHash).First(&apiKey).Error; err != nil {
		return nil, errors.New("invalid API key")
	}

	// Check if expired
	if apiKey.ExpiresAt != nil && time.Now().After(*apiKey.ExpiresAt) {
		return nil, errors.New("API key expired")
	}

	// Update last used
	s.db.Model(&apiKey).Update("last_used_at", time.Now())

	// Get the user
	var user models.User
	if err := s.db.First(&user, apiKey.UserID).Error; err != nil {
		return nil, err
	}

	// Check if user is active
	if !user.IsActive {
		return nil, errors.New("user account is disabled")
	}

	return &user, nil
}

// DeleteAPIKey permanently deletes an API key.
func (s *APIKeyService) DeleteAPIKey(_ context.Context, id uuid.UUID) error {
	result := s.db.Delete(&models.APIKey{}, id)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return errors.New("API key not found")
	}
	return nil
}
