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
	"sync"
	"time"

	"github.com/gitduppy/gitduppy/internal/database"
	"github.com/gitduppy/gitduppy/internal/metrics"
	"github.com/gitduppy/gitduppy/internal/models"
	"github.com/gitduppy/gitduppy/pkg/crypto"
	"github.com/gitduppy/gitduppy/pkg/netguard"
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
	// transport carries every outbound delivery. It refuses, at dial time,
	// connections to loopback, private, link-local and metadata addresses
	// unless private targets were explicitly allowed (SetAllowPrivateTargets).
	transport *http.Transport

	// Outbound deliveries (events and tests) run on a fixed pool of workers fed
	// by a bounded queue, so neither event bursts nor repeated test requests can
	// spawn unbounded goroutines. ctx is cancelled by Stop to abort in-flight
	// deliveries on shutdown.
	queue    chan webhookJob
	ctx      context.Context
	cancel   context.CancelFunc
	workers  sync.WaitGroup
	stopOnce sync.Once
}

// Delivery limits. retry_count is the total number of attempts per delivery.
const (
	webhookDefaultRetryCount   = 3
	webhookMaxRetryCount       = 10
	webhookDefaultTimeout      = 30 // seconds
	webhookMaxTimeoutSeconds   = 120
	webhookDeliveryDeadline    = 10 * time.Minute
	webhookDeliveryWorkers     = 8
	webhookDeliveryQueueLength = 256
	// webhookDBTimeout bounds each delivery-record query. It is derived from
	// the caller's context without its cancellation, so the final status write
	// still happens during shutdown, but a stalled database cannot block a worker (and Stop) forever.
	webhookDBTimeout = 10 * time.Second
)

// dbCtx returns a context bounding one delivery-record query. It keeps
// parent's values but not its cancellation, so the final status write still
// happens during shutdown.
func dbCtx(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(parent), webhookDBTimeout)
}

// ErrWebhookQueueFull is returned when a delivery cannot be queued because the
// bounded delivery queue is full or the service is shutting down.
var ErrWebhookQueueFull = errors.New("webhook delivery queue is full, try again later")

// webhookJob is one queued delivery.
type webhookJob struct {
	webhook   models.WebhookConfig
	eventType string
	payload   map[string]any
}

// NewWebhookService creates a new webhook service. encryption may be nil, in
// which case secrets are stored as-is (used only where no master key is wired).
// Call Stop on shutdown to cancel in-flight deliveries.
func NewWebhookService(cloneService *CloneService, encryption *crypto.EncryptionService) *WebhookService {
	s := &WebhookService{
		db:           database.GetDB(),
		cloneService: cloneService,
		encryption:   encryption,
		transport:    netguard.Policy{}.Transport(),
	}
	s.startDeliveryWorkers(webhookDeliveryWorkers, webhookDeliveryQueueLength)
	return s
}

// startDeliveryWorkers creates the delivery queue and its worker pool.
func (s *WebhookService) startDeliveryWorkers(workers, queueLen int) {
	s.ctx, s.cancel = context.WithCancel(context.Background())
	s.queue = make(chan webhookJob, queueLen)
	for range workers {
		s.workers.Add(1)
		go func() {
			defer s.workers.Done()
			for {
				select {
				case <-s.ctx.Done():
					return
				case job := <-s.queue:
					// select picks randomly among ready cases, so a job can
					// still be received after Stop; honour Stop's contract
					// of dropping queued deliveries.
					if s.ctx.Err() != nil {
						return
					}
					s.deliverWebhook(s.ctx, job.webhook, job.eventType, job.payload)
				}
			}
		}()
	}
}

// Stop cancels in-flight deliveries, waits for the workers to exit and drops
// anything still queued. Safe to call more than once.
func (s *WebhookService) Stop() {
	s.stopOnce.Do(func() {
		if s.cancel == nil {
			return
		}
		s.cancel()
		s.workers.Wait()
		if n := len(s.queue); n > 0 {
			zap.L().Named("webhook-service").Warn("dropping queued webhook deliveries on shutdown", zap.Int("count", n))
		}
	})
}

// enqueue hands a delivery to the worker pool without blocking.
func (s *WebhookService) enqueue(job webhookJob) error {
	if s.ctx == nil || s.ctx.Err() != nil {
		return ErrWebhookQueueFull
	}
	select {
	case s.queue <- job:
		return nil
	default:
		return ErrWebhookQueueFull
	}
}

// validateDeliveryLimits checks user-supplied retry_count/timeout_seconds. Zero
// means "use the default"; values outside the bounds are rejected.
func validateDeliveryLimits(retryCount, timeoutSeconds *int) error {
	if retryCount != nil && (*retryCount < 0 || *retryCount > webhookMaxRetryCount) {
		return fmt.Errorf("%w: retry_count must be between 0 and %d", ErrValidation, webhookMaxRetryCount)
	}
	if timeoutSeconds != nil && (*timeoutSeconds < 0 || *timeoutSeconds > webhookMaxTimeoutSeconds) {
		return fmt.Errorf("%w: timeout_seconds must be between 0 and %d", ErrValidation, webhookMaxTimeoutSeconds)
	}
	return nil
}

// clampAttempts maps a stored retry_count onto the attempt budget actually used,
// so rows written before validation existed cannot schedule unbounded retries.
func clampAttempts(retryCount int) int {
	switch {
	case retryCount < 1:
		return webhookDefaultRetryCount
	case retryCount > webhookMaxRetryCount:
		return webhookMaxRetryCount
	default:
		return retryCount
	}
}

// clampTimeout maps a stored timeout_seconds onto the per-attempt timeout used.
func clampTimeout(timeoutSeconds int) time.Duration {
	switch {
	case timeoutSeconds <= 0:
		timeoutSeconds = webhookDefaultTimeout
	case timeoutSeconds > webhookMaxTimeoutSeconds:
		timeoutSeconds = webhookMaxTimeoutSeconds
	}
	return time.Duration(timeoutSeconds) * time.Second
}

// SetAllowPrivateTargets lets webhook deliveries reach loopback and private
// network addresses (security.allow_private_network_targets). Link-local and
// cloud metadata addresses stay blocked. Call once at startup.
func (s *WebhookService) SetAllowPrivateTargets(allow bool) {
	s.transport = netguard.Policy{AllowPrivate: allow, AllowLoopback: allow}.Transport()
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
			return nil, fmt.Errorf("%w: webhook", ErrNotFound)
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
	if err := validateDeliveryLimits(&req.RetryCount, &req.TimeoutSeconds); err != nil {
		return nil, err
	}
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
		CreatedAt:      time.Now().UTC(),
		UpdatedAt:      time.Now().UTC(),
	}

	if webhook.RetryCount <= 0 {
		webhook.RetryCount = webhookDefaultRetryCount
	}
	if webhook.TimeoutSeconds <= 0 {
		webhook.TimeoutSeconds = webhookDefaultTimeout
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
	if err := validateDeliveryLimits(req.RetryCount, req.TimeoutSeconds); err != nil {
		return nil, err
	}
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

	webhook.UpdatedAt = time.Now().UTC()
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
		return fmt.Errorf("%w: webhook", ErrNotFound)
	}
	return nil
}

// SendEvent sends a webhook event to all subscribed webhooks.
func (s *WebhookService) SendEvent(ctx context.Context, eventType string, payload map[string]any) error {
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
		// Deliveries must outlive the triggering request, so they are queued to
		// the service's worker pool (bounded, cancelled on Stop) rather than tied
		// to ctx. A full queue drops the delivery with an error log instead of
		// blocking the caller (e.g. the clone worker).
		if err := s.enqueue(webhookJob{webhook: webhook, eventType: eventType, payload: payload}); err != nil {
			zap.L().Named("webhook-service").Error("dropping webhook delivery",
				zap.String("webhook_id", webhook.ID.String()), zap.String("event", eventType), zap.Error(err))
			// Keep the drop visible in the deliveries listing.
			payloadJSON, mErr := json.Marshal(payload)
			if mErr != nil {
				payloadJSON = []byte("{}")
			}
			s.recordTerminalFailure(ctx, webhook.ID, eventType, string(payloadJSON), "delivery dropped: "+err.Error())
		}
	}

	return nil
}

// webhookMaxRetryDelay caps the exponential backoff between webhook delivery
// attempts so a large configured retry count cannot schedule an unbounded sleep.
const webhookMaxRetryDelay = 5 * time.Minute

// webhookRetryDelay returns the backoff to wait after the given (1-based) failed
// attempt using an exponential 5x schedule (1s, 5s, 25s, ...) capped at
// webhookMaxRetryDelay.
func webhookRetryDelay(attempt int) time.Duration {
	d := time.Second
	for i := 1; i < attempt; i++ {
		d *= 5
		if d >= webhookMaxRetryDelay {
			return webhookMaxRetryDelay
		}
	}
	return d
}

// deliverWebhook delivers a webhook payload to a single webhook, retrying failed
// attempts with exponential backoff up to the webhook's configured attempt
// budget. A single WebhookDelivery row tracks the delivery across every attempt:
// its attempt count, HTTP status, response/error and status are updated in place,
// and after the final failed attempt the row is left in the terminal "failed"
// (permanently failed) state, visible via the deliveries listing.
//
// The whole delivery, retries and backoff included, is bounded by
// webhookDeliveryDeadline and aborted when parent is cancelled (Stop).
func (s *WebhookService) deliverWebhook(parent context.Context, webhook models.WebhookConfig, eventType string, payload map[string]any) {
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		s.recordTerminalFailure(parent, webhook.ID, eventType,
			fmt.Sprintf("{\"marshal_error\": %q}", err.Error()), "payload marshal failed: "+err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(parent, webhookDeliveryDeadline)
	defer cancel()

	// Clamp the stored retry count: 0/negative still delivers (default budget)
	// and rows saved before validation existed cannot exceed the maximum.
	retries := clampAttempts(webhook.RetryCount)

	// One row per delivery, created up front (status "pending") and updated on
	// each attempt so the listing shows a single evolving record that ends in a
	// terminal status rather than one row per attempt.
	delivery := &models.WebhookDelivery{
		ID:              uuid.New(),
		WebhookConfigID: webhook.ID,
		EventType:       eventType,
		Payload:         string(payloadJSON),
		Status:          "pending",
		Success:         false,
		AttemptNumber:   0,
		DeliveredAt:     time.Now().UTC(),
	}
	cctx, ccancel := dbCtx(parent)
	err = s.db.WithContext(cctx).Create(delivery).Error
	ccancel()
	if err != nil {
		zap.L().Named("webhook-service").Error("failed to create webhook delivery record",
			zap.String("webhook_id", webhook.ID.String()), zap.Error(err))
		return
	}

	for attempt := 1; attempt <= retries; attempt++ {
		// The job carries a snapshot taken when the event was queued. Re-read
		// the webhook before every attempt so disabling or deleting it, or
		// changing its URL or secret, takes effect for queued and retrying
		// deliveries too: nothing is sent to a destination no longer configured.
		current, reason := s.reloadWebhook(ctx, webhook.ID, eventType)
		if current == nil {
			s.updateDelivery(ctx, delivery, attempt-1, 0, "delivery cancelled: "+reason, false, true)
			return
		}
		webhook = *current
		httpStatus, detail, ok := s.attemptDelivery(ctx, webhook, eventType, payloadJSON, attempt)
		// The row becomes terminal once an attempt succeeds, the budget is spent,
		// or the delivery deadline/shutdown leaves no time for another attempt.
		aborted := !ok && ctx.Err() != nil
		if aborted {
			detail += " (delivery aborted: deadline exceeded or shutting down)"
		}
		s.updateDelivery(ctx, delivery, attempt, httpStatus, detail, ok, ok || aborted || attempt == retries)
		if ok || aborted || attempt == retries {
			return
		}
		timer := time.NewTimer(webhookRetryDelay(attempt))
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			s.updateDelivery(ctx, delivery, attempt, httpStatus,
				detail+" (delivery aborted before retry: deadline exceeded or shutting down)", false, true)
			return
		}
	}
}

// reloadWebhook returns the webhook's current configuration, or nil plus a
// reason when it was deleted or (except for explicit test deliveries)
// deactivated, or cannot be read.
func (s *WebhookService) reloadWebhook(parent context.Context, id uuid.UUID, eventType string) (*models.WebhookConfig, string) {
	ctx, cancel := dbCtx(parent)
	defer cancel()
	var current models.WebhookConfig
	if err := s.db.WithContext(ctx).Where("id = ?", id).Take(&current).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, "webhook no longer exists"
		}
		return nil, "could not load webhook configuration"
	}
	if !current.IsActive && eventType != "webhook.test" {
		return nil, "webhook is disabled"
	}
	return &current, ""
}

// attemptDelivery attempts a single webhook delivery, returning the HTTP status
// (0 when the request never completed), a human-readable status/error detail, and
// whether it succeeded. It does not touch the DB; the caller records the outcome.
func (s *WebhookService) attemptDelivery(parent context.Context, webhook models.WebhookConfig, eventType string, payloadJSON []byte, attempt int) (httpStatus int, detail string, success bool) {
	// Clamp the timeout: a stored 0 (or negative) would make context.WithTimeout
	// an already-expired deadline, failing every delivery instantly, so fall back
	// to the default; oversized legacy values are capped.
	timeout := clampTimeout(webhook.TimeoutSeconds)
	// parent is the worker's delivery context (deadline + shutdown), never the
	// triggering request's context: deliveries outlive the request.
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "POST", webhook.URL, bytes.NewBuffer(payloadJSON))
	if err != nil {
		return 0, err.Error(), false
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GitMirrors-Event", eventType)
	req.Header.Set("X-GitMirrors-Delivery-Attempt", strconv.Itoa(attempt))

	// Add HMAC signature if a secret is configured (decrypt the at-rest secret
	// first). If the stored secret cannot be decrypted, fail safe: report a failed
	// delivery and do NOT send. Sending unsigned would silently downgrade a
	// signature-protected webhook to an unauthenticated one for any receiver that
	// only verifies when a signature is present.
	secret, decErr := s.decryptSecret(webhook.Secret)
	if decErr != nil {
		zap.L().Named("webhook-service").Error("cannot decrypt webhook secret; skipping delivery (not sending unsigned)",
			zap.String("webhook_id", webhook.ID.String()), zap.Error(decErr))
		return 0, "webhook secret could not be decrypted", false
	}
	if secret != "" {
		signature := s.generateHMACSignature(payloadJSON, secret)
		req.Header.Set("X-GitMirrors-Signature", signature)
	}

	// Give the client an explicit timeout in addition to the request context so a
	// zero-value client can never wait without bound (item 27). Match it to the
	// per-webhook timeout (30s default; webhook delivery warrants a longer bound
	// than the 10s used for other outbound calls).
	// Redirects are not followed so a permitted host cannot bounce the delivery
	// to an internal address; the transport enforces the address policy.
	transport := s.transport
	if transport == nil {
		transport = netguard.Policy{}.Transport()
	}
	client := &http.Client{
		Timeout:       timeout,
		Transport:     transport,
		CheckRedirect: netguard.NoRedirect,
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err.Error(), false
	}
	defer resp.Body.Close()

	success = resp.StatusCode >= 200 && resp.StatusCode < 300
	return resp.StatusCode, resp.Status, success
}

// generateHMACSignature generates an HMAC signature for the payload.
func (s *WebhookService) generateHMACSignature(payload []byte, secret string) string {
	h := hmac.New(sha256.New, []byte(secret))
	h.Write(payload)
	return "sha256=" + hex.EncodeToString(h.Sum(nil))
}

// updateDelivery records the outcome of one attempt onto the delivery row,
// moving it to a terminal status once the attempt succeeded ("success") or the
// retry budget is exhausted ("failed"); otherwise it stays "pending".
func (s *WebhookService) updateDelivery(ctx context.Context, delivery *models.WebhookDelivery, attempt, httpStatus int, detail string, success, terminal bool) {
	status := "pending"
	switch {
	case success:
		status = "success"
	case terminal:
		status = "failed"
	}

	delivery.AttemptNumber = attempt
	delivery.HTTPStatus = &httpStatus
	delivery.ResponseBody = detail
	delivery.Success = success
	delivery.Status = status
	delivery.DeliveredAt = time.Now().UTC()

	uctx, ucancel := dbCtx(ctx)
	defer ucancel()
	if err := s.db.WithContext(uctx).Model(delivery).Updates(map[string]any{
		"attempt_number": attempt,
		"http_status":    httpStatus,
		"response_body":  detail,
		"success":        success,
		"status":         status,
		"delivered_at":   delivery.DeliveredAt,
	}).Error; err != nil {
		zap.L().Named("webhook-service").Error("failed to update webhook delivery record",
			zap.String("delivery_id", delivery.ID.String()), zap.Error(err))
	}

	if terminal {
		outcome := "failed"
		if success {
			outcome = "success"
		}
		metrics.WebhookDeliveriesTotal.WithLabelValues(outcome).Inc()
	}
}

// recordTerminalFailure records a delivery that never left the process (its
// payload could not be marshalled, or it was dropped because the delivery
// queue was full) as a single terminal "failed" row, so the failure is still
// visible in the deliveries listing.
func (s *WebhookService) recordTerminalFailure(parent context.Context, webhookID uuid.UUID, eventType, payload, detail string) {
	status := 0
	delivery := &models.WebhookDelivery{
		ID:              uuid.New(),
		WebhookConfigID: webhookID,
		EventType:       eventType,
		Payload:         payload,
		HTTPStatus:      &status,
		ResponseBody:    detail,
		Success:         false,
		Status:          "failed",
		AttemptNumber:   1,
		DeliveredAt:     time.Now().UTC(),
	}
	ctx, cancel := dbCtx(parent)
	defer cancel()
	if err := s.db.WithContext(ctx).Create(delivery).Error; err != nil {
		zap.L().Named("webhook-service").Error("failed to record terminal webhook delivery failure",
			zap.String("webhook_id", webhookID.String()), zap.Error(err))
	}
	metrics.WebhookDeliveriesTotal.WithLabelValues("failed").Inc()
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

	payload := map[string]any{
		"event":      "webhook.test",
		"message":    "This is a test webhook delivery",
		"timestamp":  time.Now().UTC().Format(time.RFC3339),
		"webhook_id": webhook.ID.String(),
	}

	// Queued to the bounded worker pool (detached from the request); a full
	// queue is reported to the caller so repeated tests cannot pile up work.
	return s.enqueue(webhookJob{webhook: *webhook, eventType: "webhook.test", payload: payload})
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
