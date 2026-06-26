package handlers

import (
	"crypto/hmac"
	"crypto/sha1" // #nosec G505
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"io"
	"log"
	"net/http"
	"sort"

	"github.com/gin-gonic/gin"
	"github.com/gitduppy/gitduppy/internal/middleware"
	"github.com/gitduppy/gitduppy/internal/models"
	"github.com/gitduppy/gitduppy/internal/services"
	"github.com/gitduppy/gitduppy/pkg/response"
	"github.com/gitduppy/gitduppy/pkg/validator"
	"github.com/google/uuid"
)

const (
	providerGitHub    = "github"
	providerGitLab    = "gitlab"
	providerBitbucket = "bitbucket"
	providerGeneric   = "generic"
)

// WebhookHandler handles webhook requests.
type WebhookHandler struct {
	webhookService *services.WebhookService
}

// NewWebhookHandler creates a new webhook handler.
func NewWebhookHandler(webhookService *services.WebhookService) *WebhookHandler {
	return &WebhookHandler{
		webhookService: webhookService,
	}
}

// ListWebhooks handles GET /api/v1/webhooks.
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

// GetWebhook handles GET /api/v1/webhooks/:id.
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

// CreateWebhook handles POST /api/v1/webhooks.
func (h *WebhookHandler) CreateWebhook(c *gin.Context) {
	user, ok := middleware.GetCurrentUser(c)
	if !ok {
		response.Unauthorized(c, "Not authenticated")
		return
	}

	var req services.CreateWebhookRequest
	if bindErr := c.ShouldBindJSON(&req); bindErr != nil {
		response.BadRequest(c, "INVALID_REQUEST", bindErr.Error())
		return
	}

	if valErr := validator.ValidateStruct(&req); valErr != nil {
		response.BadRequest(c, "VALIDATION_ERROR", valErr.Error())
		return
	}

	webhook, err := h.webhookService.CreateWebhook(c, user.ID, &req)
	if err != nil {
		response.BadRequest(c, "CREATE_ERROR", err.Error())
		return
	}

	response.Created(c, webhook)
}

// UpdateWebhook handles PUT /api/v1/webhooks/:id.
func (h *WebhookHandler) UpdateWebhook(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "INVALID_ID", "Invalid webhook ID format")
		return
	}

	var req services.UpdateWebhookRequest
	if bindErr := c.ShouldBindJSON(&req); bindErr != nil {
		response.BadRequest(c, "INVALID_REQUEST", bindErr.Error())
		return
	}

	webhook, err := h.webhookService.UpdateWebhook(c, id, &req)
	if err != nil {
		response.BadRequest(c, "UPDATE_ERROR", err.Error())
		return
	}

	response.Success(c, webhook)
}

// DeleteWebhook handles DELETE /api/v1/webhooks/:id.
func (h *WebhookHandler) DeleteWebhook(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "INVALID_ID", "Invalid webhook ID format")
		return
	}

	if deleteErr := h.webhookService.DeleteWebhook(c, id); deleteErr != nil {
		response.BadRequest(c, "DELETE_ERROR", deleteErr.Error())
		return
	}

	response.SuccessWithMessage(c, "Webhook deleted", nil)
}

// GetWebhookDeliveries handles GET /api/v1/webhooks/:id/deliveries.
func (h *WebhookHandler) GetWebhookDeliveries(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "INVALID_ID", "Invalid webhook ID format")
		return
	}

	limit := validator.ParseInt(c.Query("limit"), 50)
	deliveries, delivErr := h.webhookService.GetWebhookDeliveries(c, id, limit)
	if delivErr != nil {
		response.InternalError(c, delivErr.Error())
		return
	}

	response.Success(c, deliveries)
}

// TestWebhook handles POST /api/v1/webhooks/:id/test.
func (h *WebhookHandler) TestWebhook(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "INVALID_ID", "Invalid webhook ID format")
		return
	}

	if testErr := h.webhookService.TestWebhook(c, id); testErr != nil {
		response.BadRequest(c, "TEST_ERROR", testErr.Error())
		return
	}

	response.SuccessWithMessage(c, "Test webhook queued", nil)
}

// ReceiveWebhook handles POST /api/v1/webhooks/receive.
func (h *WebhookHandler) ReceiveWebhook(c *gin.Context) {
	// Limit body to 1MB to prevent unbounded memory use.
	const maxBodySize = 1 << 20 // 1 MB
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBodySize)

	// Read raw body for signature verification.
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		response.BadRequest(c, "INVALID_REQUEST", "Failed to read request body")
		return
	}

	// Try to find matching webhook by URL or custom headers.
	webhook, provider, matchErr := h.findMatchingWebhook(c, body)
	if matchErr != nil {
		response.NotFound(c, "No matching webhook found")
		return
	}

	// Verify HMAC signature if webhook has a secret.
	if webhook.Secret != "" {
		if !h.verifySignature(c, webhook.Secret, body, provider) {
			response.Unauthorized(c, "Invalid signature")
			return
		}
	}

	// Parse payload based on provider.
	_, parseErr := h.parseWebhookPayload(provider, body)
	if parseErr != nil {
		response.BadRequest(c, "INVALID_PAYLOAD", "Failed to parse webhook payload: "+parseErr.Error())
		return
	}

	// Trigger clone jobs for matching repositories.
	if triggerErr := h.triggerCloneJobs(c, webhook, body); triggerErr != nil {
		response.InternalError(c, "Failed to trigger clone jobs: "+triggerErr.Error())
		return
	}

	response.SuccessWithMessage(c, "Webhook processed successfully", nil)
}

// findMatchingWebhook finds a webhook that matches the incoming request.
func (h *WebhookHandler) findMatchingWebhook(c *gin.Context, body []byte) (*models.WebhookConfig, string, error) {
	if c.GetHeader("X-GitHub-Event") != "" {
		return h.matchProvider(c, providerGitHub, body, h.matchesGitHubWebhook)
	}
	if c.GetHeader("X-Gitlab-Event") != "" {
		return h.matchProvider(c, providerGitLab, body, h.matchesGitLabWebhook)
	}
	if c.GetHeader("X-Event-Key") != "" {
		return h.matchProvider(c, providerBitbucket, body, h.matchesBitbucketWebhook)
	}
	return h.matchProvider(c, providerGeneric, body, h.matchesGenericWebhook)
}

func (h *WebhookHandler) matchProvider(c *gin.Context, provider string, body []byte, matchFunc func(*models.WebhookConfig, *gin.Context, []byte) bool) (*models.WebhookConfig, string, error) {
	var webhooks []models.WebhookConfig
	if err := h.webhookService.DB().Where("provider = ? AND is_active = ?", provider, true).Find(&webhooks).Error; err != nil {
		return nil, "", err
	}
	var matches []*models.WebhookConfig
	for i := range webhooks {
		wh := &webhooks[i]
		if matchFunc(wh, c, body) {
			matches = append(matches, wh)
		}
	}
	sort.Slice(matches, func(i, j int) bool {
		return matches[i].CreatedAt.After(matches[j].CreatedAt)
	})
	if len(matches) == 0 {
		return nil, "", fmt.Errorf("no matching webhook found for provider %s", provider)
	}
	if len(matches) > 1 {
		log.Printf("Warning: Multiple webhooks matched for provider %s", provider)
	}
	return matches[0], provider, nil
}

// matchesGitHubWebhook checks if the request matches a GitHub webhook.
func (h *WebhookHandler) matchesGitHubWebhook(wh *models.WebhookConfig, c *gin.Context, body []byte) bool {
	if c.GetHeader("X-GitHub-Event") == "" {
		return false
	}
	return h.matchWebhookByRepoURL(wh, body)
}

// matchesGitLabWebhook checks if the request matches a GitLab webhook.
func (h *WebhookHandler) matchesGitLabWebhook(wh *models.WebhookConfig, c *gin.Context, body []byte) bool {
	if c.GetHeader("X-Gitlab-Event") == "" {
		return false
	}
	return h.matchWebhookByRepoURL(wh, body)
}

// matchesBitbucketWebhook checks if the request matches a Bitbucket webhook.
func (h *WebhookHandler) matchesBitbucketWebhook(wh *models.WebhookConfig, _ *gin.Context, body []byte) bool {
	return h.matchWebhookByRepoURL(wh, body)
}

// matchesGenericWebhook checks if the request matches a generic webhook.
func (h *WebhookHandler) matchesGenericWebhook(wh *models.WebhookConfig, _ *gin.Context, body []byte) bool {
	return h.matchWebhookByRepoURL(wh, body)
}

// matchWebhookByRepoURL matches incoming webhook payload's repository URL
// against the webhook config's RepositoryID or URLPattern.
// If the config has neither, it is treated as a catch-all.
func (h *WebhookHandler) matchWebhookByRepoURL(wh *models.WebhookConfig, body []byte) bool {
	// If webhook is scoped to a specific repository or URL pattern, validate
	// against the payload's repository URL.
	if wh.RepositoryID != nil || wh.URLPattern != "" {
		repoURL := extractRepoURLFromPayload(body)
		if repoURL == "" {
			return false
		}

		// If RepositoryID is set, ensure the payload's repoURL corresponds to that repository.
		if wh.RepositoryID != nil {
			var repo models.Repository
			if err := h.webhookService.DB().First(&repo, wh.RepositoryID).Error; err != nil {
				return false
			}
			if !containsFold(repoURL, repo.URL) && !containsFold(repo.URL, repoURL) {
				return false
			}
		}

		if wh.URLPattern != "" {
			if !containsFold(repoURL, wh.URLPattern) {
				return false
			}
		}
	}
	return true
}

// extractRepoURLFromPayload tries to pull the repository clone URL from a
// provider webhook JSON payload.
func extractRepoURLFromPayload(body []byte) string {
	type Link struct {
		Href string `json:"href"`
	}
	type BitbucketCloneLink struct {
		Name string `json:"name"`
		Href string `json:"href"`
	}
	var payload struct {
		Project struct {
			GitHTTPURL string `json:"git_http_url"`
			GitSSHURL  string `json:"git_ssh_url"`
			WebURL     string `json:"web_url"`
			URL        string `json:"url"`
		} `json:"project"`
		Repository struct {
			CloneURL   string `json:"clone_url"`
			HTMLURL    string `json:"html_url"`
			URL        string `json:"url"`
			CloneURLBB string `json:"cloneUrl"` // Bitbucket Server
			Links      struct {
				Clone []BitbucketCloneLink `json:"clone"`
				HTML  Link                 `json:"html"`
				Self  Link                 `json:"self"`
			} `json:"links"`
		} `json:"repository"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return ""
	}

	// 1. Try GitLab project fields
	if payload.Project.GitHTTPURL != "" {
		return payload.Project.GitHTTPURL
	}
	if payload.Project.GitSSHURL != "" {
		return payload.Project.GitSSHURL
	}
	if payload.Project.WebURL != "" {
		return payload.Project.WebURL
	}
	if payload.Project.URL != "" {
		return payload.Project.URL
	}

	// 2. Try Bitbucket links metadata
	for _, cloneLink := range payload.Repository.Links.Clone {
		if cloneLink.Href != "" {
			return cloneLink.Href
		}
	}
	if payload.Repository.Links.HTML.Href != "" {
		return payload.Repository.Links.HTML.Href
	}
	if payload.Repository.Links.Self.Href != "" {
		return payload.Repository.Links.Self.Href
	}
	if payload.Repository.CloneURLBB != "" {
		return payload.Repository.CloneURLBB
	}

	// 3. Fall back to standard repository fields
	if payload.Repository.CloneURL != "" {
		return payload.Repository.CloneURL
	}
	if payload.Repository.HTMLURL != "" {
		return payload.Repository.HTMLURL
	}
	return payload.Repository.URL
}

// containsFold is a case-insensitive strings.Contains.
func containsFold(s, substr string) bool {
	if len(substr) == 0 {
		return true
	}
	return containsFoldSimple(s, substr)
}

func containsFoldSimple(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if equalFold(s[i:i+len(substr)], substr) {
			return true
		}
	}
	return false
}

func equalFold(a, b string) bool {
	for i := 0; i < len(a); i++ {
		ca, cb := a[i], b[i]
		if ca >= 'A' && ca <= 'Z' {
			ca += 'a' - 'A'
		}
		if cb >= 'A' && cb <= 'Z' {
			cb += 'a' - 'A'
		}
		if ca != cb {
			return false
		}
	}
	return true
}

// verifySignature verifies the HMAC signature of the webhook.
func (h *WebhookHandler) verifySignature(c *gin.Context, secret string, body []byte, provider string) bool {
	var signature string
	var hashFunc func() hash.Hash

	switch provider {
	case providerGitHub:
		signature = c.GetHeader("X-Hub-Signature-256")
		if signature == "" {
			signature = c.GetHeader("X-Hub-Signature")
			hashFunc = sha1.New
		} else {
			hashFunc = sha256.New
			signature = signature[7:] // Remove "sha256=" prefix
		}
	case providerGitLab:
		signature = c.GetHeader("X-Gitlab-Token")
		// GitLab uses simple token comparison, not HMAC
		return signature == secret
	case providerBitbucket:
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

	if provider == providerGitLab {
		// GitLab uses simple token, not HMAC
		return signature == secret
	}

	// Verify HMAC
	mac := hmac.New(hashFunc, []byte(secret))
	mac.Write(body)
	expectedMAC := hex.EncodeToString(mac.Sum(nil))

	return expectedMAC == signature
}

// parseWebhookPayload parses the webhook payload based on provider.
func (h *WebhookHandler) parseWebhookPayload(_ string, body []byte) (map[string]interface{}, error) {
	var payload map[string]interface{}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	return payload, nil
}

// triggerCloneJobs triggers clone jobs for repositories matching the webhook.
func (h *WebhookHandler) triggerCloneJobs(c *gin.Context, webhook *models.WebhookConfig, body []byte) error {
	repoURL := extractRepoURLFromPayload(body)
	if repoURL == "" {
		return fmt.Errorf("missing repository URL in payload")
	}

	// If webhook is associated with a specific repository
	if webhook.RepositoryID != nil {
		_, err := h.webhookService.CloneService().CreateCloneJob(c, *webhook.RepositoryID, "webhook")
		return err
	}

	// If webhook has a URL pattern, find the specific matching repository
	if webhook.URLPattern != "" {
		var repo models.Repository
		if err := h.webhookService.DB().WithContext(c).Where("url = ?", repoURL).First(&repo).Error; err != nil {
			return fmt.Errorf("repository not found for URL %s: %w", repoURL, err)
		}
		_, err := h.webhookService.CloneService().CreateCloneJob(c, repo.ID, "webhook")
		return err
	}

	return nil
}
