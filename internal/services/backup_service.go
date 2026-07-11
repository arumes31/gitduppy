package services

import (
	"context"
	"encoding/json"
	"fmt"
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
	exportData := map[string]any{
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

// ImportData imports configuration data. Configuration import is not yet
// implemented; rather than silently reporting success for a no-op, it returns
// ErrNotImplemented so callers surface an honest error.
func (s *BackupService) ImportData(_ context.Context, _ []byte, _ ExportFormat) error {
	return fmt.Errorf("%w: configuration import is not implemented", ErrNotImplemented)
}

// DatabaseBackup creates a database backup file. A real implementation would
// shell out to pg_dump (or similar) for PostgreSQL; that is not yet done, so it
// returns ErrNotImplemented rather than creating an empty placeholder file that
// would misrepresent a backup as having succeeded.
func (s *BackupService) DatabaseBackup(_ context.Context) (string, error) {
	return "", fmt.Errorf("%w: database backup is not implemented", ErrNotImplemented)
}
