package services

import (
	"context"
	"encoding/json"
	"time"

	"github.com/gitduppy/gitduppy/internal/database"
	"github.com/gitduppy/gitduppy/internal/models"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// AuditService handles audit logging.
type AuditService struct {
	db *gorm.DB
}

// NewAuditService creates a new audit service.
func NewAuditService() *AuditService {
	return &AuditService{
		db: database.GetDB(),
	}
}

// AuditFilter represents filters for listing audit logs.
type AuditFilter struct {
	UserID       *uuid.UUID
	RepositoryID *uuid.UUID
	Action       *string
	StartDate    *time.Time
	EndDate      *time.Time
	Page         int
	PerPage      int
}

// ListAuditLogs returns a paginated list of audit logs.
func (s *AuditService) ListAuditLogs(_ context.Context, filter *AuditFilter) ([]models.AuditLog, int64, error) {
	if filter == nil {
		filter = &AuditFilter{Page: 1, PerPage: 50}
	}
	if filter.Page < 1 {
		filter.Page = 1
	}
	if filter.PerPage < 1 {
		filter.PerPage = 50
	}

	query := s.db.Model(&models.AuditLog{}).Preload("User").Preload("Repository")

	// Apply filters
	if filter.UserID != nil {
		query = query.Where("user_id = ?", *filter.UserID)
	}
	if filter.RepositoryID != nil {
		query = query.Where("repository_id = ?", *filter.RepositoryID)
	}
	if filter.Action != nil && *filter.Action != "" {
		query = query.Where("action = ?", *filter.Action)
	}
	if filter.StartDate != nil {
		query = query.Where("created_at >= ?", *filter.StartDate)
	}
	if filter.EndDate != nil {
		query = query.Where("created_at <= ?", *filter.EndDate)
	}

	// Get total count
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	// Get paginated results
	offset := (filter.Page - 1) * filter.PerPage
	var logs []models.AuditLog
	err := query.Offset(offset).Limit(filter.PerPage).Order("created_at DESC").Find(&logs).Error
	return logs, total, err
}

// Log creates a new audit log entry.
func (s *AuditService) Log(_ context.Context, userID *uuid.UUID, repositoryID *uuid.UUID, action string, details interface{}, ipAddress, userAgent string) error {
	var detailsJSON string
	if details != nil {
		bytes, err := json.Marshal(details)
		if err != nil {
			detailsJSON = "{}"
		} else {
			detailsJSON = string(bytes)
		}
	}

	log := &models.AuditLog{
		ID:           uuid.New(),
		UserID:       userID,
		RepositoryID: repositoryID,
		Action:       action,
		Details:      detailsJSON,
		IPAddress:    &ipAddress,
		UserAgent:    &userAgent,
		CreatedAt:    time.Now(),
	}

	return s.db.Create(log).Error
}

// LogAction logs a user action with the given context.
func (s *AuditService) LogAction(ctx context.Context, userID *uuid.UUID, repositoryID *uuid.UUID, action string, details map[string]interface{}, c interface{}) error {
	var ipAddress, userAgent string

	// Try to extract IP and UserAgent from gin context if available
	if ginCtx, ok := c.(interface{ ClientIP() string }); ok {
		ipAddress = ginCtx.ClientIP()
	}

	return s.Log(ctx, userID, repositoryID, action, details, ipAddress, userAgent)
}

// GetUserActions retrieves all actions for a specific user.
func (s *AuditService) GetUserActions(_ context.Context, userID uuid.UUID, limit int) ([]models.AuditLog, error) {
	if limit <= 0 {
		limit = 100
	}

	var logs []models.AuditLog
	err := s.db.Where("user_id = ?", userID).
		Order("created_at DESC").
		Limit(limit).
		Find(&logs).Error
	return logs, err
}

// GetRepositoryActions retrieves all actions for a specific repository.
func (s *AuditService) GetRepositoryActions(_ context.Context, repositoryID uuid.UUID, limit int) ([]models.AuditLog, error) {
	if limit <= 0 {
		limit = 100
	}

	var logs []models.AuditLog
	err := s.db.Where("repository_id = ?", repositoryID).
		Order("created_at DESC").
		Limit(limit).
		Find(&logs).Error
	return logs, err
}

// CleanupOldLogs deletes audit logs older than the specified duration.
func (s *AuditService) CleanupOldLogs(_ context.Context, olderThan time.Duration) (int64, error) {
	cutoff := time.Now().Add(-olderThan)
	result := s.db.Where("created_at < ?", cutoff).Delete(&models.AuditLog{})
	return result.RowsAffected, result.Error
}
