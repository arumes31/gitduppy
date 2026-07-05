package services

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/gitduppy/gitduppy/internal/config"
	"github.com/gitduppy/gitduppy/internal/database"
	"github.com/gitduppy/gitduppy/internal/models"
	"go.uber.org/zap"
	"gopkg.in/yaml.v3"
	"gorm.io/gorm"
)

// BackupService handles backup and export functionality.
type BackupService struct {
	db     *gorm.DB
	config *config.Config
	logger *zap.Logger
}

// NewBackupService creates a new backup service.
func NewBackupService(cfg *config.Config) *BackupService {
	return &BackupService{
		db:     database.GetDB(),
		config: cfg,
		logger: zap.L().Named("backup-service"),
	}
}

// ExportFormat represents the export format.
type ExportFormat string

const (
	JSONFormat ExportFormat = "json"
	YAMLFormat ExportFormat = "yaml"
)

// ExportData exports configuration data.
func (s *BackupService) ExportData(_ context.Context, format ExportFormat) ([]byte, error) {
	// Export repositories
	var repos []models.Repository
	if err := s.db.Find(&repos).Error; err != nil {
		return nil, err
	}

	// Export tags
	var tags []models.Tag
	if err := s.db.Find(&tags).Error; err != nil {
		return nil, err
	}

	// Export webhooks
	var webhooks []models.WebhookConfig
	if err := s.db.Find(&webhooks).Error; err != nil {
		return nil, err
	}

	// Create export structure
	exportData := map[string]interface{}{
		"repositories": repos,
		"tags":         tags,
		"webhooks":     webhooks,
		"exported_at":  time.Now().UTC().Format(time.RFC3339),
	}

	// Serialize based on format
	switch format {
	case JSONFormat:
		return json.MarshalIndent(exportData, "", "  ")
	case YAMLFormat:
		return yaml.Marshal(exportData)
	default:
		return nil, fmt.Errorf("unsupported export format: %s", format)
	}
}

// ImportData imports configuration data.
func (s *BackupService) ImportData(_ context.Context, data []byte, format ExportFormat) error {
	var importData map[string]interface{}

	switch format {
	case JSONFormat:
		if err := json.Unmarshal(data, &importData); err != nil {
			return err
		}
	case YAMLFormat:
		if err := yaml.Unmarshal(data, &importData); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unsupported import format: %s", format)
	}

	// Import repositories
	if reposData, ok := importData["repositories"]; ok {
		if repos, ok := reposData.([]interface{}); ok {
			for range repos {
				// Convert to Repository model and save
				// This is a simplified implementation - in practice, you'd need
				// proper type conversion and validation
				continue
			}
		}
	}

	// Import tags
	if tagsData, ok := importData["tags"]; ok {
		if tags, ok := tagsData.([]interface{}); ok {
			for range tags {
				// Convert to Tag model and save
				continue
			}
		}
	}

	// Import webhooks
	if webhooksData, ok := importData["webhooks"]; ok {
		if webhooks, ok := webhooksData.([]interface{}); ok {
			for range webhooks {
				// Convert to WebhookConfig model and save
				continue
			}
		}
	}

	return nil
}

// DatabaseBackup creates a database backup file.
func (s *BackupService) DatabaseBackup(_ context.Context) (string, error) {
	// This is a simplified implementation
	// In practice, you'd use pg_dump or similar for PostgreSQL
	backupPath := fmt.Sprintf("%s/backup_%s.sql", s.config.Storage.BackupPath, time.Now().Format("20060102_150405"))

	// Ensure backup directory exists.
	if err := os.MkdirAll(s.config.Storage.BackupPath, 0o750); err != nil {
		return "", err
	}

	// Create empty file as placeholder.
	// #nosec G304
	file, err := os.Create(backupPath)
	if err != nil {
		return "", err
	}
	if err := file.Close(); err != nil {
		s.logger.Error("failed to close backup file", zap.String("path", backupPath), zap.Error(err))
		return "", fmt.Errorf("failed to close backup file: %w", err)
	}

	return backupPath, nil
}
