package services

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/gitduppy/gitduppy/internal/database"
	"github.com/gitduppy/gitduppy/internal/models"
	"gorm.io/gorm"
)

// HealthService handles health monitoring.
type HealthService struct {
	db *gorm.DB
}

// NewHealthService creates a new health service.
func NewHealthService() *HealthService {
	return &HealthService{
		db: database.GetDB(),
	}
}

// CheckGitServerHealth checks the health of a git server.
func (s *HealthService) CheckGitServerHealth(ctx context.Context, url string) (*models.HealthCheck, error) {
	startTime := time.Now()

	// Create HTTP client with timeout
	client := &http.Client{
		Timeout: 10 * time.Second,
	}

	req, reqErr := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)

	var resp *http.Response
	var doErr error

	if reqErr == nil {
		resp, doErr = client.Do(req)
	}

	endTime := time.Now()

	var status string
	var errorMessage string

	if reqErr != nil {
		status = "failed"
		errorMessage = reqErr.Error()
	} else if doErr != nil {
		status = "failed"
		errorMessage = doErr.Error()
	} else {
		defer resp.Body.Close()
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			status = "healthy"
		} else {
			status = "unhealthy"
			errorMessage = fmt.Sprintf("HTTP %d", resp.StatusCode)
		}
	}

	responseTimeMs := int(endTime.Sub(startTime).Milliseconds())

	healthCheck := &models.HealthCheck{
		TargetURL:      url,
		Status:         status,
		ResponseTimeMs: &responseTimeMs,
		ErrorMessage:   &errorMessage,
		CheckedAt:      endTime,
	}

	// Save to database
	if err := s.db.Create(healthCheck).Error; err != nil {
		return nil, err
	}

	return healthCheck, nil
}

// DB returns the database connection.
func (s *HealthService) DB() *gorm.DB {
	return s.db
}

// GetLatestHealthChecks returns the latest health checks for all URLs.
func (s *HealthService) GetLatestHealthChecks(_ context.Context) ([]models.HealthCheck, error) {
	var healthChecks []models.HealthCheck

	// Get distinct URLs and their latest health check
	subQuery := s.db.Model(&models.HealthCheck{}).
		Select("target_url, MAX(checked_at) as max_checked_at").
		Group("target_url")

	err := s.db.Table("health_checks hc").
		Joins("JOIN (?) sq ON hc.target_url = sq.target_url AND hc.checked_at = sq.max_checked_at", subQuery).
		Find(&healthChecks).Error

	return healthChecks, err
}

// CleanupOldHealthChecks removes health checks older than the specified duration.
func (s *HealthService) CleanupOldHealthChecks(_ context.Context, olderThan time.Duration) error {
	cutoffTime := time.Now().Add(-olderThan)
	return s.db.Where("checked_at < ?", cutoffTime).Delete(&models.HealthCheck{}).Error
}
