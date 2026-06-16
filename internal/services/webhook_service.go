package services

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gitduppy/gitduppy/internal/database"
	"github.com/gitduppy/gitduppy/internal/models"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// WebhookService handles webhook configuration and delivery.
type WebhookService struct {
	db           *gorm.DB
	cloneService *CloneService
}

// NewWebhookService creates a new webhook service.
func NewWebhookService(cloneService *CloneService) *WebhookService {
	return &WebhookService{
		db:           database.GetDB(),
		cloneService: cloneService,
	}
}

// WebhookFilter represents filters for listing webhooks.
type WebhookFilter struct {
	IsActive *bool
	Page     int
	PerPage  int
}

// ListWebhooks returns a paginated list of webhooks.
func (s *WebhookService) ListWebhooks(_ context.Context, filter *WebhookFilter) ([]models.WebhookConfig, int64, error) {
	if filter == nil {
		filter = &WebhookFilter{Page: 1, PerPage: 20}
	}
	if filter.Page < 1 {
		filter.Page = 1
	}
	if filter.PerPage < 1 {
		filter.PerPage = 20
	}

	query := s.db.Model(&models.WebhookConfig{})

	if filter.IsActive != nil {
		query = query.Where("is_active = ?", *filter.IsActive)
	}

	// Get total count
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	// Get paginated results
	offset := (filter.Page - 1) * filter.PerPage
	var webhooks []models.WebhookConfig
	err := query.Offset(offset).Limit(filter.PerPage).Order("created_at DESC").Find(&webhooks).Error
	return webhooks, total, err
}

// GetWebhookByID retrieves a webhook by ID.
func (s *WebhookService) GetWebhookByID(_ context.Context, id uuid.UUID) (*models.WebhookConfig, error) {
	var webhook models.WebhookConfig
	if err := s.db.First(&webhook, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("webhook not found")
		}
		return nil, err
	}
	return &webhook, nil
}

// CreateWebhookRequest represents a create webhook request.
type CreateWebhookRequest struct {
	Name           string   `json:"name" validate:"required"`
	URL            string   `json:"url" validate:"required,url"`
	Secret         string   `json:"secret,omitempty"`
	Events         []string `json:"events" validate:"required"`
	IsActive       bool     `json:"is_active"`
	RetryCount     int      `json:"retry_count"`
	TimeoutSeconds int      `json:"timeout_seconds"`
}

// CreateWebhook creates a new webhook.
func (s *WebhookService) CreateWebhook(_ context.Context, userID uuid.UUID, req *CreateWebhookRequest) (*models.WebhookConfig, error) {
	webhook := &models.WebhookConfig{
		ID:             uuid.New(),
		UserID:         userID,
		Name:           req.Name,
		URL:            req.URL,
		Secret:         req.Secret,
		Events:         req.Events,
		IsActive:       req.IsActive,
		RetryCount:     req.RetryCount,
		TimeoutSeconds: req.TimeoutSeconds,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}

	if webhook.RetryCount <= 0 {
		webhook.RetryCount = 3
	}
	if webhook.TimeoutSeconds <= 0 {
		webhook.TimeoutSeconds = 30
	}

	if err := s.db.Create(webhook).Error; err != nil {
		return nil, err
	}

	return webhook, nil
}

// UpdateWebhookRequest represents an update webhook request.
type UpdateWebhookRequest struct {
	Name           *string  `json:"name,omitempty"`
	URL            *string  `json:"url,omitempty"`
	Secret         *string  `json:"secret,omitempty"`
	Events         []string `json:"events,omitempty"`
	IsActive       *bool    `json:"is_active,omitempty"`
	RetryCount     *int     `json:"retry_count,omitempty"`
	TimeoutSeconds *int     `json:"timeout_seconds,omitempty"`
}

// UpdateWebhook updates a webhook.
func (s *WebhookService) UpdateWebhook(ctx context.Context, id uuid.UUID, req *UpdateWebhookRequest) (*models.WebhookConfig, error) {
	webhook, err := s.GetWebhookByID(ctx, id)
	if err != nil {
		return nil, err
	}

	if req.Name != nil {
		webhook.Name = *req.Name
	}
	if req.URL != nil {
		webhook.URL = *req.URL
	}
	if req.Secret != nil {
		webhook.Secret = *req.Secret
	}
	if req.Events != nil {
		webhook.Events = req.Events
	}
	if req.IsActive != nil {
		webhook.IsActive = *req.IsActive
	}
	if req.RetryCount != nil {
		webhook.RetryCount = *req.RetryCount
	}
	if req.TimeoutSeconds != nil {
		webhook.TimeoutSeconds = *req.TimeoutSeconds
	}

	webhook.UpdatedAt = time.Now()
	if err := s.db.Save(webhook).Error; err != nil {
		return nil, err
	}

	return webhook, nil
}

// DeleteWebhook deletes a webhook.
func (s *WebhookService) DeleteWebhook(_ context.Context, id uuid.UUID) error {
	result := s.db.Delete(&models.WebhookConfig{}, id)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return errors.New("webhook not found")
	}
	return nil
}

// SendEvent sends a webhook event to all subscribed webhooks.
func (s *WebhookService) SendEvent(_ context.Context, eventType string, payload map[string]interface{}) error {
	var webhooks []models.WebhookConfig
	if err := s.db.Where("is_active = ? AND events @> ?", true, []string{eventType}).Find(&webhooks).Error; err != nil {
		return err
	}

	for _, webhook := range webhooks {
		go s.deliverWebhook(webhook, eventType, payload)
	}

	return nil
}

// deliverWebhook delivers a webhook payload to a single webhook.
func (s *WebhookService) deliverWebhook(webhook models.WebhookConfig, eventType string, payload map[string]interface{}) {
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		// Log error if payload cannot be marshaled.
		return
	}

	for attempt := 1; attempt <= webhook.RetryCount; attempt++ {
		success := s.attemptDelivery(webhook, eventType, payloadJSON, attempt)
		if success {
			break
		}
		time.Sleep(time.Duration(attempt) * time.Second)
	}
}

// attemptDelivery attempts a single webhook delivery.
func (s *WebhookService) attemptDelivery(webhook models.WebhookConfig, eventType string, payloadJSON []byte, attempt int) bool {
	// Create request.
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(webhook.TimeoutSeconds)*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "POST", webhook.URL, bytes.NewBuffer(payloadJSON))
	if err != nil {
		s.recordDelivery(webhook.ID, eventType, string(payloadJSON), 0, err.Error(), false, attempt)
		return false
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GitMirrors-Event", eventType)
	req.Header.Set("X-GitMirrors-Delivery-Attempt", strconv.Itoa(attempt))

	// Add HMAC signature if secret is set.
	if webhook.Secret != "" {
		signature := s.generateHMACSignature(payloadJSON, webhook.Secret)
		req.Header.Set("X-GitMirrors-Signature", signature)
	}

	// Send request.
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		s.recordDelivery(webhook.ID, eventType, string(payloadJSON), 0, err.Error(), false, attempt)
		return false
	}
	defer resp.Body.Close()

	success := resp.StatusCode >= 200 && resp.StatusCode < 300
	s.recordDelivery(webhook.ID, eventType, string(payloadJSON), resp.StatusCode, resp.Status, success, attempt)
	return success
}

// generateHMACSignature generates an HMAC signature for the payload.
func (s *WebhookService) generateHMACSignature(payload []byte, secret string) string {
	h := hmac.New(sha256.New, []byte(secret))
	h.Write(payload)
	return "sha256=" + hex.EncodeToString(h.Sum(nil))
}

// recordDelivery records a webhook delivery attempt.
func (s *WebhookService) recordDelivery(webhookID uuid.UUID, eventType, payload string, httpStatus int, responseBody string, success bool, attempt int) {
	delivery := &models.WebhookDelivery{
		ID:              uuid.New(),
		WebhookConfigID: webhookID,
		EventType:       eventType,
		Payload:         payload,
		HTTPStatus:      &httpStatus,
		ResponseBody:    responseBody,
		Success:         success,
		AttemptNumber:   attempt,
		DeliveredAt:     time.Now(),
	}
	s.db.Create(delivery)
}

// GetWebhookDeliveries retrieves deliveries for a webhook.
func (s *WebhookService) GetWebhookDeliveries(_ context.Context, webhookID uuid.UUID, limit int) ([]models.WebhookDelivery, error) {
	if limit <= 0 {
		limit = 50
	}

	var deliveries []models.WebhookDelivery
	err := s.db.Where("webhook_config_id = ?", webhookID).
		Order("delivered_at DESC").
		Limit(limit).
		Find(&deliveries).Error
	return deliveries, err
}

// TestWebhook sends a test webhook event.
func (s *WebhookService) TestWebhook(ctx context.Context, webhookID uuid.UUID) error {
	webhook, err := s.GetWebhookByID(ctx, webhookID)
	if err != nil {
		return err
	}

	payload := map[string]interface{}{
		"event":      "webhook.test",
		"message":    "This is a test webhook delivery",
		"timestamp":  time.Now().UTC().Format(time.RFC3339),
		"webhook_id": webhook.ID.String(),
	}

	go s.deliverWebhook(*webhook, "webhook.test", payload)
	return nil
}

// DB returns the database connection.
func (s *WebhookService) DB() *gorm.DB {
	return s.db
}

// CloneService returns the clone service instance.
func (s *WebhookService) CloneService() *CloneService {
	return s.cloneService
}

// FindRepositoriesByURLPattern finds repositories matching a URL pattern.
func (s *WebhookService) FindRepositoriesByURLPattern(_ context.Context, pattern string) ([]models.Repository, error) {
	var repos []models.Repository
	// Simple implementation - match URL containing the pattern
	err := s.db.Where("url LIKE ?", "%"+pattern+"%").Find(&repos).Error
	return repos, err
}
