package services

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gitduppy/gitduppy/internal/database"
	"github.com/gitduppy/gitduppy/internal/metrics"
	"github.com/gitduppy/gitduppy/internal/models"
	"github.com/gitduppy/gitduppy/pkg/crypto"
	"github.com/google/uuid"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// encSecretPrefix tags a webhook secret that is stored encrypted at rest, so
// legacy plaintext secrets (written before encryption) remain readable.
const encSecretPrefix = "enc:"

// WebhookService handles webhook configuration and delivery.
type WebhookService struct {
	db           *gorm.DB
	cloneService *CloneService
	encryption   *crypto.EncryptionService
}

// NewWebhookService creates a new webhook service. encryption may be nil, in
// which case secrets are stored as-is (used only where no master key is wired).
func NewWebhookService(cloneService *CloneService, encryption *crypto.EncryptionService) *WebhookService {
	return &WebhookService{
		db:           database.GetDB(),
		cloneService: cloneService,
		encryption:   encryption,
	}
}

// encryptSecret returns the at-rest representation of a webhook secret. Empty
// secrets stay empty; otherwise the value is AES-encrypted and prefix-tagged. If
// encryption fails it falls back to storing plaintext (so the secret is not lost)
// but logs an error so operators can tell it was persisted unencrypted.
func (s *WebhookService) encryptSecret(secret string) string {
	if secret == "" || s.encryption == nil {
		return secret
	}
	ct, err := s.encryption.EncryptString(secret)
	if err != nil {
		zap.L().Named("webhook-service").Error("failed to encrypt webhook secret; storing it UNENCRYPTED (plaintext) as a fallback", zap.Error(err))
		return secret
	}
	return encSecretPrefix + ct
}

// DecryptSecret exposes the at-rest secret in usable plaintext for callers
// outside this package (e.g. the incoming-webhook signature verifier). A prefixed
// value that cannot be decrypted returns an error rather than a bogus secret.
func (s *WebhookService) DecryptSecret(stored string) (string, error) {
	return s.decryptSecret(stored)
}

// decryptSecret returns the usable secret from its at-rest representation,
// transparently handling legacy plaintext values that lack the prefix. A value
// tagged as encrypted that fails to decrypt returns ("", error) so callers never
// mistake the raw ciphertext for the real secret.
func (s *WebhookService) decryptSecret(stored string) (string, error) {
	if !strings.HasPrefix(stored, encSecretPrefix) {
		// Truly legacy plaintext secret (written before encryption existed).
		return stored, nil
	}
	// The value is tagged as encrypted. If encryption is not wired we cannot
	// recover the plaintext, so fail loudly rather than hand back the raw
	// ciphertext (which would silently be used as if it were the real secret).
	if s.encryption == nil {
		return "", fmt.Errorf("decrypt webhook secret: value is encrypted but encryption is disabled")
	}
	pt, err := s.encryption.DecryptString(strings.TrimPrefix(stored, encSecretPrefix))
	if err != nil {
		return "", fmt.Errorf("decrypt webhook secret: %w", err)
	}
	return pt, nil
}

// WebhookFilter represents filters for listing webhooks.
type WebhookFilter struct {
	IsActive *bool
	// UserID, when set, restricts the listing to webhooks owned by that user.
	// It is left nil for admins so they can see every webhook.
	UserID  *uuid.UUID
	Page    int
	PerPage int
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
	if filter.PerPage > 200 {
		filter.PerPage = 200
	}

	query := s.db.Model(&models.WebhookConfig{})

	if filter.UserID != nil {
		query = query.Where("user_id = ?", *filter.UserID)
	}

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
		Secret:         s.encryptSecret(req.Secret),
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
		webhook.Secret = s.encryptSecret(*req.Secret)
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
	// events is a JSONB array column, so the containment operand must be a JSON
	// array literal (e.g. ["clone.failed"]). Passing a Go []string encodes a
	// Postgres text[] instead, which errors with "invalid input syntax for type
	// json" and silently drops every webhook match.
	eventJSON, err := json.Marshal([]string{eventType})
	if err != nil {
		return err
	}
	var webhooks []models.WebhookConfig
	if err := s.db.Where("is_active = ? AND events @> ?::jsonb", true, string(eventJSON)).Find(&webhooks).Error; err != nil {
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
		// Record marshal failure in delivery history so it is visible.
		s.recordDelivery(webhook.ID, eventType, fmt.Sprintf("{\"marshal_error\": %q}", err.Error()), 0, "payload marshal failed: "+err.Error(), false, 1)
		return
	}

	// Floor the retry count so a webhook stored/updated with 0 (or a negative
	// value — UpdateWebhook applies no lower bound) still delivers at least once
	// instead of silently dropping the event.
	retries := webhook.RetryCount
	if retries < 1 {
		retries = 3
	}
	for attempt := 1; attempt <= retries; attempt++ {
		success := s.attemptDelivery(webhook, eventType, payloadJSON, attempt)
		if success {
			break
		}
		time.Sleep(time.Duration(attempt) * time.Second)
	}
}

// attemptDelivery attempts a single webhook delivery.
func (s *WebhookService) attemptDelivery(webhook models.WebhookConfig, eventType string, payloadJSON []byte, attempt int) bool {
	// Create request. Floor the timeout: a stored 0 (or negative) would make
	// context.WithTimeout an already-expired deadline, failing every delivery
	// instantly, so fall back to the default 30s.
	timeoutSeconds := webhook.TimeoutSeconds
	if timeoutSeconds <= 0 {
		timeoutSeconds = 30
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSeconds)*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "POST", webhook.URL, bytes.NewBuffer(payloadJSON))
	if err != nil {
		s.recordDelivery(webhook.ID, eventType, string(payloadJSON), 0, err.Error(), false, attempt)
		return false
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GitMirrors-Event", eventType)
	req.Header.Set("X-GitMirrors-Delivery-Attempt", strconv.Itoa(attempt))

	// Add HMAC signature if a secret is configured (decrypt the at-rest secret
	// first). If the stored secret cannot be decrypted, fail safe: record a failed
	// delivery and do NOT send. Sending unsigned would silently downgrade a
	// signature-protected webhook to an unauthenticated one for any receiver that
	// only verifies when a signature is present.
	secret, decErr := s.decryptSecret(webhook.Secret)
	if decErr != nil {
		zap.L().Named("webhook-service").Error("cannot decrypt webhook secret; skipping delivery (not sending unsigned)",
			zap.String("webhook_id", webhook.ID.String()), zap.Error(decErr))
		s.recordDelivery(webhook.ID, eventType, string(payloadJSON), 0, "webhook secret could not be decrypted", false, attempt)
		return false
	}
	if secret != "" {
		signature := s.generateHMACSignature(payloadJSON, secret)
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

	outcome := "failed"
	if success {
		outcome = "success"
	}
	metrics.WebhookDeliveriesTotal.WithLabelValues(outcome).Inc()
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
