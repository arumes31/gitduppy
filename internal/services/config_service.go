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

	// Retrieve dynamic OAuth settings if present in the database. Load all six
	// keys in one query rather than six sequential round-trips.
	cfg.OAuth.GitHub = s.oauthProviderFromDB(ctx, cfg.OAuth.GitHub, "github")
	cfg.OAuth.GitLab = s.oauthProviderFromDB(ctx, cfg.OAuth.GitLab, "gitlab")
	cfg.OAuth.Google = s.oauthProviderFromDB(ctx, cfg.OAuth.Google, "google")

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

// getSettingStrings loads several settings in a single query, decrypting any
// encrypted values, and returns them keyed by setting key (missing keys are
// simply absent). It replaces a run of per-key First() round-trips.
func (s *ConfigService) getSettingStrings(ctx context.Context, keys ...string) map[string]string {
	out := make(map[string]string, len(keys))
	if s.db == nil || len(keys) == 0 {
		return out
	}
	var rows []models.SystemSetting
	if err := s.db.WithContext(ctx).Where("key IN ?", keys).Find(&rows).Error; err != nil {
		return out
	}
	for _, r := range rows {
		val := r.Value
		if r.IsEncrypted && val != "" {
			// Never expose raw ciphertext: an encrypted value must be successfully
			// decrypted. If decryption is unavailable or fails, skip the key rather
			// than storing the ciphertext as if it were the plaintext value.
			if s.encryptionService == nil {
				continue
			}
			dec, err := s.encryptionService.DecryptString(val)
			if err != nil {
				continue
			}
			val = dec
		}
		out[r.Key] = val
	}
	return out
}

// oauthProviderFromDB overlays DB-stored client id/secret (when present) onto the
// static config for a provider whose settings keys are oauth2_<name>_client_id
// and oauth2_<name>_client_secret.
func (s *ConfigService) oauthProviderFromDB(ctx context.Context, base config.OAuthProviderConfig, name string) config.OAuthProviderConfig {
	idKey := "oauth2_" + name + "_client_id"
	secretKey := "oauth2_" + name + "_client_secret"
	m := s.getSettingStrings(ctx, idKey, secretKey)
	if v := m[idKey]; v != "" {
		base.ClientID = v
	}
	if v := m[secretKey]; v != "" {
		base.ClientSecret = v
	}
	return base
}

// GetGitHubOAuth retrieves the active GitHub OAuth configuration.
func (s *ConfigService) GetGitHubOAuth(ctx context.Context) config.OAuthProviderConfig {
	return s.oauthProviderFromDB(ctx, s.config.OAuth.GitHub, "github")
}

// GetGitLabOAuth retrieves the active GitLab OAuth configuration.
func (s *ConfigService) GetGitLabOAuth(ctx context.Context) config.OAuthProviderConfig {
	return s.oauthProviderFromDB(ctx, s.config.OAuth.GitLab, "gitlab")
}

// GetGoogleOAuth retrieves the active Google OAuth configuration.
func (s *ConfigService) GetGoogleOAuth(ctx context.Context) config.OAuthProviderConfig {
	return s.oauthProviderFromDB(ctx, s.config.OAuth.Google, "google")
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
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	}

	// Use FirstOrCreate logic or Update
	var existing models.SystemSetting
	if err := tx.WithContext(ctx).Where("key = ?", key).First(&existing).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return tx.WithContext(ctx).Create(&setting).Error
		}
		return err
	}

	return tx.WithContext(ctx).Model(&existing).Updates(map[string]any{
		"value":        storeValue,
		"is_encrypted": isEncrypted,
		"description":  description,
		"updated_at":   time.Now().UTC(),
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
