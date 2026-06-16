package services

import (
	"context"

	"github.com/gitduppy/gitduppy/internal/config"
)

// ConfigService handles configuration management
type ConfigService struct {
	config *config.Config
}

// NewConfigService creates a new config service
func NewConfigService(cfg *config.Config) *ConfigService {
	return &ConfigService{
		config: cfg,
	}
}

// GetConfig returns the current configuration (with sensitive fields masked)
func (s *ConfigService) GetConfig(ctx context.Context) *config.Config {
	// Create a copy to avoid modifying original
	cfg := *s.config

	// Mask sensitive fields
	cfg.Database.Password = "***"
	cfg.Security.MasterKey = "***"
	cfg.Security.SessionSecret = "***"
	cfg.Security.CSRFKey = "***"

	// Mask OAuth secrets
	if cfg.OAuth.GitHub.ClientSecret != "" {
		cfg.OAuth.GitHub.ClientSecret = "***"
	}
	if cfg.OAuth.GitLab.ClientSecret != "" {
		cfg.OAuth.GitLab.ClientSecret = "***"
	}
	if cfg.OAuth.Google.ClientSecret != "" {
		cfg.OAuth.Google.ClientSecret = "***"
	}

	// Mask email password
	if cfg.Email.SMTPPassword != "" {
		cfg.Email.SMTPPassword = "***"
	}

	return &cfg
}

// UpdateConfig updates the configuration (requires restart)
func (s *ConfigService) UpdateConfig(ctx context.Context, newConfig *config.Config) error {
	// This is a simplified implementation
	// In practice, you'd need to validate the new config,
	// save it to a file, and signal the main process to restart
	return nil
}
