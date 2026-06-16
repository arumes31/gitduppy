package handlers

import (
	"github.com/gin-gonic/gin"
	"github.com/gitduppy/gitduppy/internal/services"
	"github.com/gitduppy/gitduppy/pkg/response"
	"github.com/gitduppy/gitduppy/pkg/validator"
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
		response.InternalError(c, err.Error())
		return
	}

	response.Success(c, stats)
}

// GetChartData handles GET /api/v1/dashboard/chart-data.
func (h *DashboardHandler) GetChartData(c *gin.Context) {
	days := validator.ParseInt(c.Query("days"), 30)
	chartData, err := h.dashboardService.GetChartData(c, days)
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}

	response.Success(c, chartData)
}

// GetTopRepositories handles GET /api/v1/dashboard/top-repositories.
func (h *DashboardHandler) GetTopRepositories(c *gin.Context) {
	limit := validator.ParseInt(c.Query("limit"), 10)
	repos, err := h.dashboardService.GetTopRepositories(c, limit)
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}

	response.Success(c, repos)
}

// GetRecentJobs handles GET /api/v1/dashboard/recent-jobs.
func (h *DashboardHandler) GetRecentJobs(c *gin.Context) {
	limit := validator.ParseInt(c.Query("limit"), 10)
	jobs, err := h.cloneService.GetRecentJobs(c, limit)
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}

	response.Success(c, jobs)
}
