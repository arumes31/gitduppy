package handlers

import (
	"github.com/gin-gonic/gin"
	"github.com/gitduppy/gitduppy/internal/models"
	"github.com/gitduppy/gitduppy/internal/services"
	"github.com/gitduppy/gitduppy/pkg/response"
	"golang.org/x/sync/errgroup"
)

// DashboardHandler handles dashboard requests.
type DashboardHandler struct {
	dashboardService *services.DashboardService
	cloneService     *services.CloneService
}

// NewDashboardHandler creates a new dashboard handler.
func NewDashboardHandler(dashboardService *services.DashboardService, cloneService *services.CloneService) *DashboardHandler {
	return &DashboardHandler{
		dashboardService: dashboardService,
		cloneService:     cloneService,
	}
}

// GetStats handles GET /api/v1/dashboard/stats.
func (h *DashboardHandler) GetStats(c *gin.Context) {
	stats, err := h.dashboardService.GetStats(c)
	if err != nil {
		respondServiceError(c, err)
		return
	}

	response.Success(c, stats)
}

// GetChartData handles GET /api/v1/dashboard/chart-data.
func (h *DashboardHandler) GetChartData(c *gin.Context) {
	days := parseLimitParam(c, "days", 30, 365)
	chartData, err := h.dashboardService.GetChartData(c, days)
	if err != nil {
		respondServiceError(c, err)
		return
	}

	response.Success(c, chartData)
}

// GetTopRepositories handles GET /api/v1/dashboard/top-repositories.
func (h *DashboardHandler) GetTopRepositories(c *gin.Context) {
	limit := parseLimitParam(c, "limit", 10, maxPerPage)
	repos, err := h.dashboardService.GetTopRepositories(c, limit)
	if err != nil {
		respondServiceError(c, err)
		return
	}

	response.Success(c, repos)
}

// GetRecentJobs handles GET /api/v1/dashboard/recent-jobs.
func (h *DashboardHandler) GetRecentJobs(c *gin.Context) {
	limit := parseLimitParam(c, "limit", 10, maxPerPage)
	jobs, err := h.cloneService.GetRecentJobs(c, limit)
	if err != nil {
		respondServiceError(c, err)
		return
	}

	response.Success(c, jobs)
}

// GetTimeline handles GET /api/v1/dashboard/timeline.
func (h *DashboardHandler) GetTimeline(c *gin.Context) {
	// Clamp to a safe range so a request cannot trigger an oversized or
	// negative fetch.
	limit := parseLimitParam(c, "limit", 50, 200)
	timeline, err := h.dashboardService.GetTimelineData(c, limit)
	if err != nil {
		respondServiceError(c, err)
		return
	}
	response.Success(c, timeline)
}

// GetOverview handles GET /api/v1/dashboard/overview. It returns the four
// independent dashboard read payloads — stats, the 5 most recent jobs, the
// timeline, and the paperbin quota — in a single response so the client makes one
// round trip instead of four. The fetches run concurrently via errgroup; the four
// existing single-purpose endpoints are left untouched for backward compatibility.
//
// Partial failure fails the whole request (respondServiceError): the payload is
// rendered as a unit, so returning three of four sections would be misleading, and
// a single error code is simpler for the caller than per-section error handling.
func (h *DashboardHandler) GetOverview(c *gin.Context) {
	const (
		recentJobsLimit = 5
		timelineLimit   = 50
	)

	var (
		stats     *services.DashboardStats
		jobs      []models.CloneJob
		timeline  []models.CloneJob
		pbSize    int64
		pbQuotaGB float64
	)

	g, ctx := errgroup.WithContext(c.Request.Context())
	g.Go(func() error {
		var err error
		stats, err = h.dashboardService.GetStats(ctx)
		return err
	})
	g.Go(func() error {
		var err error
		jobs, err = h.cloneService.GetRecentJobs(ctx, recentJobsLimit)
		return err
	})
	g.Go(func() error {
		var err error
		timeline, err = h.dashboardService.GetTimelineData(ctx, timelineLimit)
		return err
	})
	g.Go(func() error {
		var err error
		pbSize, pbQuotaGB, err = h.dashboardService.GetPaperbinSize(ctx)
		return err
	})

	if err := g.Wait(); err != nil {
		respondServiceError(c, err)
		return
	}

	quotaBytes := int64(pbQuotaGB * 1024 * 1024 * 1024)
	response.Success(c, gin.H{
		"stats":       stats,
		"recent_jobs": jobs,
		"timeline":    timeline,
		"paperbin_quota": gin.H{
			"size_bytes":  pbSize,
			"quota_gb":    pbQuotaGB,
			"quota_bytes": quotaBytes,
			"exceeded":    pbSize > quotaBytes,
		},
	})
}

// GetPaperbinQuota handles GET /api/v1/dashboard/paperbin-quota.
func (h *DashboardHandler) GetPaperbinQuota(c *gin.Context) {
	sizeBytes, quotaGB, err := h.dashboardService.GetPaperbinSize(c)
	if err != nil {
		respondServiceError(c, err)
		return
	}

	quotaBytes := int64(quotaGB * 1024 * 1024 * 1024)
	exceeded := sizeBytes > quotaBytes

	response.Success(c, gin.H{
		"size_bytes":  sizeBytes,
		"quota_gb":    quotaGB,
		"quota_bytes": quotaBytes,
		"exceeded":    exceeded,
	})
}
