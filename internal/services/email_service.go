package services

import (
	"context"
	"fmt"
	"time"

	"github.com/gitduppy/gitduppy/internal/config"
	"github.com/gitduppy/gitduppy/internal/database"
	"github.com/gitduppy/gitduppy/internal/models"
	"github.com/gitduppy/gitduppy/pkg/email"
	"gorm.io/gorm"
)

// EmailService handles email notifications
type EmailService struct {
	db     *gorm.DB
	email  *email.EmailService
	config *config.Config
}

// NewEmailService creates a new email service
func NewEmailService(cfg *config.Config) *EmailService {
	return &EmailService{
		db:     database.GetDB(),
		email:  email.NewEmailService(&cfg.Email),
		config: cfg,
	}
}

// IsEnabled returns true if email notifications are enabled
func (s *EmailService) IsEnabled() bool {
	return s.email.IsEnabled()
}

// SendCloneFailureNotification sends a notification for clone failure
func (s *EmailService) SendCloneFailureNotification(ctx context.Context, repo *models.Repository, job *models.CloneJob, err error) error {
	if !s.IsEnabled() {
		return nil
	}

	// Get admin users to notify
	adminUsers, err := s.getAdminUsers(ctx)
	if err != nil {
		return err
	}

	if len(adminUsers) == 0 {
		return nil // No admins to notify
	}

	// Prepare recipient list
	var recipients []string
	for _, user := range adminUsers {
		if user.Email != "" {
			recipients = append(recipients, user.Email)
		}
	}

	if len(recipients) == 0 {
		return nil // No valid email addresses
	}

	// Render template
	data := email.TemplateData{
		AppName:    "GitDuppy",
		Repository: repo,
		CloneJob:   job,
		Error:      err.Error(),
		Timestamp:  time.Now().Format("2006-01-02 15:04:05"),
		AdminEmail: s.config.Email.From,
		BaseURL:    s.config.Server.BaseURL,
	}

	body, err := s.email.RenderTemplate("clone_failure", data)
	if err != nil {
		return err
	}

	subject := fmt.Sprintf("GitDuppy - Clone Failure: %s", repo.Name)
	return s.email.SendEmail(recipients, subject, body)
}

// SendSystemErrorNotification sends a notification for system errors
func (s *EmailService) SendSystemErrorNotification(ctx context.Context, errorMessage string) error {
	if !s.IsEnabled() {
		return nil
	}

	adminUsers, err := s.getAdminUsers(ctx)
	if err != nil {
		return err
	}

	if len(adminUsers) == 0 {
		return nil
	}

	var recipients []string
	for _, user := range adminUsers {
		if user.Email != "" {
			recipients = append(recipients, user.Email)
		}
	}

	if len(recipients) == 0 {
		return nil
	}

	data := email.TemplateData{
		AppName:    "GitDuppy",
		Error:      errorMessage,
		Timestamp:  time.Now().Format("2006-01-02 15:04:05"),
		AdminEmail: s.config.Email.From,
		BaseURL:    s.config.Server.BaseURL,
	}

	body, err := s.email.RenderTemplate("system_error", data)
	if err != nil {
		return err
	}

	subject := "GitDuppy - System Error"
	return s.email.SendEmail(recipients, subject, body)
}

// getAdminUsers retrieves all active admin users
func (s *EmailService) getAdminUsers(ctx context.Context) ([]models.User, error) {
	var users []models.User
	err := s.db.Where("role = ? AND is_active = ?", "admin", true).Find(&users).Error
	return users, err
}
