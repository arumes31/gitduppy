package services

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/gitduppy/gitduppy/internal/database"
	"github.com/gitduppy/gitduppy/internal/models"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// APIKeyService handles API key CRUD operations.
type APIKeyService struct {
	db        *gorm.DB
	authCache AuthCacheInvalidator
}

// NewAPIKeyService creates a new API key service.
func NewAPIKeyService() *APIKeyService {
	return &APIKeyService{
		db: database.GetDB(),
	}
}

// SetAuthCache wires the middleware auth cache so revoking an API key evicts its
// cached entry eagerly rather than letting the revoked key authenticate for the
// cache TTL. Optional: a nil invalidator simply falls back to TTL expiry.
func (s *APIKeyService) SetAuthCache(cache AuthCacheInvalidator) {
	s.authCache = cache
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
func (s *APIKeyService) CreateAPIKey(ctx context.Context, userID uuid.UUID, req *CreateAPIKeyRequest) (*CreateAPIKeyResponse, error) {
	if req.ExpiresInDays < 0 {
		return nil, fmt.Errorf("%w: expires_in_days must not be negative", ErrValidation)
	}

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
		exp := time.Now().UTC().Add(time.Duration(req.ExpiresInDays) * 24 * time.Hour)
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
		CreatedAt: time.Now().UTC(),
	}

	if err := s.db.WithContext(ctx).Create(apiKey).Error; err != nil {
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
func (s *APIKeyService) ListAPIKeys(ctx context.Context, userID uuid.UUID) ([]models.APIKey, error) {
	var keys []models.APIKey
	err := s.db.WithContext(ctx).Where("user_id = ?", userID).Order("created_at DESC").Find(&keys).Error
	return keys, err
}

// GetAPIKeyByID retrieves an API key by ID.
func (s *APIKeyService) GetAPIKeyByID(ctx context.Context, id uuid.UUID) (*models.APIKey, error) {
	var key models.APIKey
	if err := s.db.WithContext(ctx).First(&key, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("%w: API key", ErrNotFound)
		}
		return nil, err
	}
	return &key, nil
}

// RevokeAPIKey revokes an API key.
func (s *APIKeyService) RevokeAPIKey(ctx context.Context, id uuid.UUID) error {
	// Load the row first so we have its key_hash for a precise cache eviction (the
	// auth cache is keyed by the stored key_hash).
	var key models.APIKey
	if err := s.db.WithContext(ctx).First(&key, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("%w: API key", ErrNotFound)
		}
		return err
	}

	if err := s.db.WithContext(ctx).Model(&models.APIKey{}).Where("id = ?", id).Update("is_active", false).Error; err != nil {
		return err
	}

	// Evict precisely by the stored key_hash so the revoked key stops
	// authenticating immediately rather than after the cache TTL.
	if s.authCache != nil {
		s.authCache.Evict(key.KeyHash)
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
	s.db.Model(&apiKey).Update("last_used_at", time.Now().UTC())

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
func (s *APIKeyService) DeleteAPIKey(ctx context.Context, id uuid.UUID) error {
	result := s.db.WithContext(ctx).Delete(&models.APIKey{}, id)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("%w: API key", ErrNotFound)
	}
	return nil
}
