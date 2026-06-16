package handlers

import (
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"io"

	"github.com/gin-gonic/gin"
	"github.com/gitduppy/gitduppy/internal/middleware"
	"github.com/gitduppy/gitduppy/internal/models"
	"github.com/gitduppy/gitduppy/internal/services"
	"github.com/gitduppy/gitduppy/pkg/response"
	"github.com/gitduppy/gitduppy/pkg/validator"
	"github.com/google/uuid"
)

// WebhookHandler handles webhook requests
type WebhookHandler struct {
	webhookService *services.WebhookService
}

// NewWebhookHandler creates a new webhook handler
func NewWebhookHandler(webhookService *services.WebhookService) *WebhookHandler {
	return &WebhookHandler{
		webhookService: webhookService,
	}
}

// ListWebhooks handles GET /api/v1/webhooks
func (h *WebhookHandler) ListWebhooks(c *gin.Context) {
	filter := &services.WebhookFilter{
		Page:    1,
		PerPage: 20,
	}

	if page := c.Query("page"); page != "" {
		filter.Page = validator.ParseInt(page, 1)
	}
	if perPage := c.Query("per_page"); perPage != "" {
		filter.PerPage = validator.ParseInt(perPage, 20)
	}

	isActive := c.Query("is_active")
	if isActive != "" {
		active := isActive == "true"
		filter.IsActive = &active
	}

	webhooks, total, err := h.webhookService.ListWebhooks(c, filter)
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}

	response.SuccessWithMeta(c, webhooks, &response.Meta{
		Page:       filter.Page,
		PerPage:    filter.PerPage,
		Total:      int(total),
		TotalPages: int(total/int64(filter.PerPage)) + 1,
	})
}

// GetWebhook handles GET /api/v1/webhooks/:id
func (h *WebhookHandler) GetWebhook(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "INVALID_ID", "Invalid webhook ID format")
		return
	}

	webhook, err := h.webhookService.GetWebhookByID(c, id)
	if err != nil {
		response.NotFound(c, "Webhook not found")
		return
	}

	response.Success(c, webhook)
}

// CreateWebhook handles POST /api/v1/webhooks
func (h *WebhookHandler) CreateWebhook(c *gin.Context) {
	user, ok := middleware.GetCurrentUser(c)
	if !ok {
		response.Unauthorized(c, "Not authenticated")
		return
	}

	var req services.CreateWebhookRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "INVALID_REQUEST", err.Error())
		return
	}

	if err := validator.ValidateStruct(&req); err != nil {
		response.BadRequest(c, "VALIDATION_ERROR", err.Error())
		return
	}

	webhook, err := h.webhookService.CreateWebhook(c, user.ID, &req)
	if err != nil {
		response.BadRequest(c, "CREATE_ERROR", err.Error())
		return
	}

	response.Created(c, webhook)
}

// UpdateWebhook handles PUT /api/v1/webhooks/:id
func (h *WebhookHandler) UpdateWebhook(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "INVALID_ID", "Invalid webhook ID format")
		return
	}

	var req services.UpdateWebhookRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "INVALID_REQUEST", err.Error())
		return
	}

	webhook, err := h.webhookService.UpdateWebhook(c, id, &req)
	if err != nil {
		response.BadRequest(c, "UPDATE_ERROR", err.Error())
		return
	}

	response.Success(c, webhook)
}

// DeleteWebhook handles DELETE /api/v1/webhooks/:id
func (h *WebhookHandler) DeleteWebhook(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "INVALID_ID", "Invalid webhook ID format")
		return
	}

	if err := h.webhookService.DeleteWebhook(c, id); err != nil {
		response.BadRequest(c, "DELETE_ERROR", err.Error())
		return
	}

	response.SuccessWithMessage(c, "Webhook deleted", nil)
}

// GetWebhookDeliveries handles GET /api/v1/webhooks/:id/deliveries
func (h *WebhookHandler) GetWebhookDeliveries(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "INVALID_ID", "Invalid webhook ID format")
		return
	}

	limit := validator.ParseInt(c.Query("limit"), 50)
	deliveries, err := h.webhookService.GetWebhookDeliveries(c, id, limit)
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}

	response.Success(c, deliveries)
}

// TestWebhook handles POST /api/v1/webhooks/:id/test
func (h *WebhookHandler) TestWebhook(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "INVALID_ID", "Invalid webhook ID format")
		return
	}

	if err := h.webhookService.TestWebhook(c, id); err != nil {
		response.BadRequest(c, "TEST_ERROR", err.Error())
		return
	}

	response.SuccessWithMessage(c, "Test webhook queued", nil)
}

// ReceiveWebhook handles POST /api/v1/webhooks/receive
func (h *WebhookHandler) ReceiveWebhook(c *gin.Context) {
	// Read raw body for signature verification
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		response.BadRequest(c, "INVALID_REQUEST", "Failed to read request body")
		return
	}

	// Try to find matching webhook by URL or custom headers
	webhook, provider, err := h.findMatchingWebhook(c, body)
	if err != nil {
		response.NotFound(c, "No matching webhook found")
		return
	}

	// Verify HMAC signature if webhook has a secret
	if webhook.Secret != "" {
		if !h.verifySignature(c, webhook.Secret, body, provider) {
			response.Unauthorized(c, "Invalid signature")
			return
		}
	}

	// Parse payload based on provider
	payload, err := h.parseWebhookPayload(provider, body)
	if err != nil {
		response.BadRequest(c, "INVALID_PAYLOAD", "Failed to parse webhook payload: "+err.Error())
		return
	}

	// Trigger clone jobs for matching repositories
	if err := h.triggerCloneJobs(c, webhook, payload); err != nil {
		response.InternalError(c, "Failed to trigger clone jobs: "+err.Error())
		return
	}

	response.SuccessWithMessage(c, "Webhook processed successfully", nil)
}

// findMatchingWebhook finds a webhook that matches the incoming request
func (h *WebhookHandler) findMatchingWebhook(c *gin.Context, body []byte) (*models.WebhookConfig, string, error) {
	// First, try to match by X-GitHub-Event header (GitHub)
	if event := c.GetHeader("X-GitHub-Event"); event != "" {
		// GitHub webhook
		var webhooks []models.WebhookConfig
		if err := h.webhookService.DB().Where("provider = ? AND is_active = ?", "github", true).Find(&webhooks).Error; err != nil {
			return nil, "", err
		}
		for _, wh := range webhooks {
			if h.matchesGitHubWebhook(&wh, c, body) {
				return &wh, "github", nil
			}
		}
	}

	// Try GitLab
	if c.GetHeader("X-Gitlab-Event") != "" {
		var webhooks []models.WebhookConfig
		if err := h.webhookService.DB().Where("provider = ? AND is_active = ?", "gitlab", true).Find(&webhooks).Error; err != nil {
			return nil, "", err
		}
		for _, wh := range webhooks {
			if h.matchesGitLabWebhook(&wh, c, body) {
				return &wh, "gitlab", nil
			}
		}
	}

	// Try Bitbucket
	if c.GetHeader("X-Event-Key") != "" {
		var webhooks []models.WebhookConfig
		if err := h.webhookService.DB().Where("provider = ? AND is_active = ?", "bitbucket", true).Find(&webhooks).Error; err != nil {
			return nil, "", err
		}
		for _, wh := range webhooks {
			if h.matchesBitbucketWebhook(&wh, c, body) {
				return &wh, "bitbucket", nil
			}
		}
	}

	// Generic webhook matching by URL pattern or custom logic
	var webhooks []models.WebhookConfig
	if err := h.webhookService.DB().Where("provider = ? AND is_active = ?", "generic", true).Find(&webhooks).Error; err != nil {
		return nil, "", err
	}
	for _, wh := range webhooks {
		if h.matchesGenericWebhook(&wh, c, body) {
			return &wh, "generic", nil
		}
	}

	return nil, "", fmt.Errorf("no matching webhook found")
}

// matchesGitHubWebhook checks if the request matches a GitHub webhook
func (h *WebhookHandler) matchesGitHubWebhook(webhook *models.WebhookConfig, c *gin.Context, body []byte) bool {
	// For GitHub, we can match by repository URL in the payload
	// This is a simplified implementation - in practice, you'd parse the payload
	// and match against webhook.RepositoryID or webhook.URLPattern
	return true // Assume match if provider matches
}

// matchesGitLabWebhook checks if the request matches a GitLab webhook
func (h *WebhookHandler) matchesGitLabWebhook(webhook *models.WebhookConfig, c *gin.Context, body []byte) bool {
	return true
}

// matchesBitbucketWebhook checks if the request matches a Bitbucket webhook
func (h *WebhookHandler) matchesBitbucketWebhook(webhook *models.WebhookConfig, c *gin.Context, body []byte) bool {
	return true
}

// matchesGenericWebhook checks if the request matches a generic webhook
func (h *WebhookHandler) matchesGenericWebhook(webhook *models.WebhookConfig, c *gin.Context, body []byte) bool {
	// Custom matching logic based on webhook configuration
	// This could include URL patterns, custom headers, etc.
	return true
}

// verifySignature verifies the HMAC signature of the webhook
func (h *WebhookHandler) verifySignature(c *gin.Context, secret string, body []byte, provider string) bool {
	var signature string
	var hashFunc func() hash.Hash

	switch provider {
	case "github":
		signature = c.GetHeader("X-Hub-Signature-256")
		if signature == "" {
			signature = c.GetHeader("X-Hub-Signature")
			hashFunc = sha1.New
		} else {
			hashFunc = sha256.New
			signature = signature[7:] // Remove "sha256=" prefix
		}
	case "gitlab":
		signature = c.GetHeader("X-Gitlab-Token")
		// GitLab uses simple token comparison, not HMAC
		return signature == secret
	case "bitbucket":
		signature = c.GetHeader("X-Hub-Signature")
		hashFunc = sha256.New
		signature = signature[7:] // Remove "sha256=" prefix
	default:
		// Generic - assume SHA256
		signature = c.GetHeader("X-Signature")
		hashFunc = sha256.New
	}

	if signature == "" {
		return false
	}

	if provider == "gitlab" {
		// GitLab uses simple token, not HMAC
		return signature == secret
	}

	// Verify HMAC
	mac := hmac.New(hashFunc, []byte(secret))
	mac.Write(body)
	expectedMAC := hex.EncodeToString(mac.Sum(nil))

	return expectedMAC == signature
}

// parseWebhookPayload parses the webhook payload based on provider
func (h *WebhookHandler) parseWebhookPayload(provider string, body []byte) (map[string]interface{}, error) {
	var payload map[string]interface{}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	return payload, nil
}

// triggerCloneJobs triggers clone jobs for repositories matching the webhook
func (h *WebhookHandler) triggerCloneJobs(c *gin.Context, webhook *models.WebhookConfig, payload map[string]interface{}) error {
	// If webhook is associated with a specific repository
	if webhook.RepositoryID != nil {
		_, err := h.webhookService.CloneService().CreateCloneJob(c, *webhook.RepositoryID, "webhook")
		return err
	}

	// If webhook has a URL pattern, find matching repositories
	if webhook.URLPattern != "" {
		// Find repositories matching the URL pattern
		repos, err := h.webhookService.FindRepositoriesByURLPattern(c, webhook.URLPattern)
		if err != nil {
			return err
		}
		for _, repo := range repos {
			_, err := h.webhookService.CloneService().CreateCloneJob(c, repo.ID, "webhook")
			if err != nil {
				// Log error but continue with other repositories
				continue
			}
		}
	}

	return nil
}
