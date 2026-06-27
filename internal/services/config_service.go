package services

import (
	"context"
	"errors"
	"time"

	"github.com/gitduppy/gitduppy/internal/config"
	"github.com/gitduppy/gitduppy/internal/models"
	"github.com/gitduppy/gitduppy/pkg/crypto"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

const mask = "***"

// ConfigService handles configuration management.
type ConfigService struct {
	config            *config.Config
	db                *gorm.DB
	encryptionService *crypto.EncryptionService
}

// NewConfigService creates a new config service.
func NewConfigService(cfg *config.Config, db *gorm.DB, encryptionService *crypto.EncryptionService) *ConfigService {
	return &ConfigService{
		config:            cfg,
		db:                db,
		encryptionService: encryptionService,
	}
}

// GetConfig returns the current configuration (with sensitive fields masked).
func (s *ConfigService) GetConfig(ctx context.Context) *config.Config {
	// Create a copy to avoid modifying original
	cfg := *s.config

	// Retrieve dynamic OAuth settings if present in the database.
	if clientID, err := s.GetSettingString(ctx, "oauth2_github_client_id"); err == nil && clientID != "" {
		cfg.OAuth.GitHub.ClientID = clientID
	}
	if clientSecret, err := s.GetSettingString(ctx, "oauth2_github_client_secret"); err == nil && clientSecret != "" {
		cfg.OAuth.GitHub.ClientSecret = clientSecret
	}
	if clientID, err := s.GetSettingString(ctx, "oauth2_gitlab_client_id"); err == nil && clientID != "" {
		cfg.OAuth.GitLab.ClientID = clientID
	}
	if clientSecret, err := s.GetSettingString(ctx, "oauth2_gitlab_client_secret"); err == nil && clientSecret != "" {
		cfg.OAuth.GitLab.ClientSecret = clientSecret
	}
	if clientID, err := s.GetSettingString(ctx, "oauth2_google_client_id"); err == nil && clientID != "" {
		cfg.OAuth.Google.ClientID = clientID
	}
	if clientSecret, err := s.GetSettingString(ctx, "oauth2_google_client_secret"); err == nil && clientSecret != "" {
		cfg.OAuth.Google.ClientSecret = clientSecret
	}

	// Mask sensitive fields
	if cfg.Database.Password != "" {
		cfg.Database.Password = mask
	}
	if cfg.Security.MasterKey != "" {
		cfg.Security.MasterKey = mask
	}
	if cfg.Security.SessionSecret != "" {
		cfg.Security.SessionSecret = mask
	}
	if cfg.Security.CSRFKey != "" {
		cfg.Security.CSRFKey = mask
	}

	// Mask OAuth secrets
	if cfg.OAuth.GitHub.ClientSecret != "" {
		cfg.OAuth.GitHub.ClientSecret = mask
	}
	if cfg.OAuth.GitLab.ClientSecret != "" {
		cfg.OAuth.GitLab.ClientSecret = mask
	}
	if cfg.OAuth.Google.ClientSecret != "" {
		cfg.OAuth.Google.ClientSecret = mask
	}

	// Mask email password
	if cfg.Email.SMTPPassword != "" {
		cfg.Email.SMTPPassword = mask
	}

	return &cfg
}

// GetGitHubOAuth retrieves the active GitHub OAuth configuration.
func (s *ConfigService) GetGitHubOAuth(ctx context.Context) config.OAuthProviderConfig {
	cfg := s.config.OAuth.GitHub

	// Fallback to database values if available
	if clientID, err := s.GetSettingString(ctx, "oauth2_github_client_id"); err == nil && clientID != "" {
		cfg.ClientID = clientID
	}
	if clientSecret, err := s.GetSettingString(ctx, "oauth2_github_client_secret"); err == nil && clientSecret != "" {
		cfg.ClientSecret = clientSecret
	}

	return cfg
}

// GetGitLabOAuth retrieves the active GitLab OAuth configuration.
func (s *ConfigService) GetGitLabOAuth(ctx context.Context) config.OAuthProviderConfig {
	cfg := s.config.OAuth.GitLab
	if clientID, err := s.GetSettingString(ctx, "oauth2_gitlab_client_id"); err == nil && clientID != "" {
		cfg.ClientID = clientID
	}
	if clientSecret, err := s.GetSettingString(ctx, "oauth2_gitlab_client_secret"); err == nil && clientSecret != "" {
		cfg.ClientSecret = clientSecret
	}
	return cfg
}

// GetGoogleOAuth retrieves the active Google OAuth configuration.
func (s *ConfigService) GetGoogleOAuth(ctx context.Context) config.OAuthProviderConfig {
	cfg := s.config.OAuth.Google
	if clientID, err := s.GetSettingString(ctx, "oauth2_google_client_id"); err == nil && clientID != "" {
		cfg.ClientID = clientID
	}
	if clientSecret, err := s.GetSettingString(ctx, "oauth2_google_client_secret"); err == nil && clientSecret != "" {
		cfg.ClientSecret = clientSecret
	}
	return cfg
}

// UpdateConfig updates the configuration (requires restart).
func (s *ConfigService) UpdateConfig(_ context.Context, _ *config.Config) error {
	return errors.New("configuration persistence is not implemented")
}

// SettingWrite describes a single setting to persist.
type SettingWrite struct {
	Key         string
	Value       string
	Description string
	Encrypt     bool
}

// SetSetting saves a setting to the database.
func (s *ConfigService) SetSetting(ctx context.Context, key, value, description string, encrypt bool) error {
	if s.db == nil {
		return errors.New("database not configured")
	}
	return s.setSettingTx(ctx, s.db, key, value, description, encrypt)
}

// SetSettings persists multiple settings atomically in a single transaction, so
// a partial failure cannot leave a related group of settings inconsistent.
func (s *ConfigService) SetSettings(ctx context.Context, writes ...SettingWrite) error {
	if s.db == nil {
		return errors.New("database not configured")
	}
	return s.db.Transaction(func(tx *gorm.DB) error {
		for _, w := range writes {
			if err := s.setSettingTx(ctx, tx, w.Key, w.Value, w.Description, w.Encrypt); err != nil {
				return err
			}
		}
		return nil
	})
}

// setSettingTx upserts a single setting using the provided gorm handle (which
// may be a transaction).
func (s *ConfigService) setSettingTx(ctx context.Context, tx *gorm.DB, key, value, description string, encrypt bool) error {
	storeValue := value
	isEncrypted := false
	if encrypt && value != "" {
		if s.encryptionService == nil {
			return errors.New("encryption service is not configured but encryption is required")
		}
		encrypted, err := s.encryptionService.EncryptString(value)
		if err != nil {
			return err
		}
		storeValue = encrypted
		isEncrypted = true
	}

	setting := models.SystemSetting{
		ID:          uuid.New(),
		Key:         key,
		Value:       storeValue,
		IsEncrypted: isEncrypted,
		Description: description,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}

	// Use FirstOrCreate logic or Update
	var existing models.SystemSetting
	if err := tx.WithContext(ctx).Where("key = ?", key).First(&existing).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return tx.WithContext(ctx).Create(&setting).Error
		}
		return err
	}

	return tx.WithContext(ctx).Model(&existing).Updates(map[string]interface{}{
		"value":        storeValue,
		"is_encrypted": isEncrypted,
		"description":  description,
		"updated_at":   time.Now(),
	}).Error
}

// GetSettingString retrieves a setting from the database as a string.
func (s *ConfigService) GetSettingString(ctx context.Context, key string) (string, error) {
	if s.db == nil {
		return "", errors.New("database not configured")
	}

	var setting models.SystemSetting
	if err := s.db.WithContext(ctx).Where("key = ?", key).First(&setting).Error; err != nil {
		return "", err
	}

	if setting.IsEncrypted && setting.Value != "" && s.encryptionService != nil {
		decrypted, err := s.encryptionService.DecryptString(setting.Value)
		if err != nil {
			return "", err
		}
		return decrypted, nil
	}

	return setting.Value, nil
}
