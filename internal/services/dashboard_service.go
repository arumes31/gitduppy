package services

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gitduppy/gitduppy/internal/database"
	"github.com/gitduppy/gitduppy/internal/models"
	"gorm.io/gorm"
)

// DashboardService handles dashboard statistics.
type DashboardService struct {
	db *gorm.DB
}

// NewDashboardService creates a new dashboard service.
func NewDashboardService() *DashboardService {
	return &DashboardService{
		db: database.GetDB(),
	}
}

// DashboardStats represents dashboard statistics.
type DashboardStats struct {
	TotalRepositories         int64            `json:"total_repositories"`
	ActiveRepositories        int64            `json:"active_repositories"`
	FailedRepositories        int64            `json:"failed_repositories"`
	TotalCloneJobs            int64            `json:"total_clone_jobs"`
	SuccessRate               float64          `json:"success_rate"`
	AverageCloneDuration      float64          `json:"average_clone_duration_seconds"`
	TotalStorageBytes         int64            `json:"total_storage_bytes"`
	RecentActivity            *RecentActivity  `json:"recent_activity"`
	CloneJobStatusBreakdown   *StatusBreakdown `json:"clone_job_status_breakdown"`
	RepositoryStatusBreakdown *StatusBreakdown `json:"repository_status_breakdown"`
}

// RecentActivity represents recent activity statistics.
type RecentActivity struct {
	ClonesLast24h   int64 `json:"clones_last_24h"`
	FailuresLast24h int64 `json:"failures_last_24h"`
	NewReposLast7d  int64 `json:"new_repos_last_7d"`
}

// StatusBreakdown represents a count by status.
type StatusBreakdown struct {
	Pending   int64 `json:"pending"`
	Running   int64 `json:"running"`
	Success   int64 `json:"success"`
	Failed    int64 `json:"failed"`
	Cancelled int64 `json:"cancelled"`
}

// GetStats returns dashboard statistics.
func (s *DashboardService) GetStats(ctx context.Context) (*DashboardStats, error) {
	stats := &DashboardStats{
		RecentActivity:            &RecentActivity{},
		CloneJobStatusBreakdown:   &StatusBreakdown{},
		RepositoryStatusBreakdown: &StatusBreakdown{},
	}

	db := s.db.WithContext(ctx)

	// Repository counts
	db.Model(&models.Repository{}).Where("is_active = ?", true).Count(&stats.TotalRepositories)
	db.Model(&models.Repository{}).Where("is_active = ? AND status = 'success'", true).Count(&stats.ActiveRepositories)
	db.Model(&models.Repository{}).Where("is_active = ? AND status = 'failed'", true).Count(&stats.FailedRepositories)

	// Clone job counts
	db.Model(&models.CloneJob{}).Count(&stats.TotalCloneJobs)

	// Success rate calculation
	var totalSuccess, totalCompleted int64
	db.Model(&models.CloneJob{}).Where("status = 'success'").Count(&totalSuccess)
	db.Model(&models.CloneJob{}).Where("status IN ?", []string{"success", "failed"}).Count(&totalCompleted)
	if totalCompleted > 0 {
		stats.SuccessRate = float64(totalSuccess) / float64(totalCompleted) * 100
	}

	// Average clone duration
	type DurationResult struct {
		AvgDuration float64
	}
	var durationResult DurationResult
	db.Model(&models.CloneJob{}).
		Select("EXTRACT(EPOCH FROM (completed_at - started_at)) as avg_duration").
		Where("status = 'success' AND started_at IS NOT NULL AND completed_at IS NOT NULL").
		Scan(&durationResult)
	stats.AverageCloneDuration = durationResult.AvgDuration

	// Recent activity
	now := time.Now()
	last24h := now.Add(-24 * time.Hour)
	last7d := now.Add(-7 * 24 * time.Hour)

	db.Model(&models.CloneJob{}).Where("created_at >= ?", last24h).Count(&stats.RecentActivity.ClonesLast24h)
	db.Model(&models.CloneJob{}).Where("status = 'failed' AND completed_at >= ?", last24h).Count(&stats.RecentActivity.FailuresLast24h)
	db.Model(&models.Repository{}).Where("created_at >= ?", last7d).Count(&stats.RecentActivity.NewReposLast7d)

	// Clone job status breakdown
	db.Model(&models.CloneJob{}).Where("status = 'pending'").Count(&stats.CloneJobStatusBreakdown.Pending)
	db.Model(&models.CloneJob{}).Where("status = 'running'").Count(&stats.CloneJobStatusBreakdown.Running)
	db.Model(&models.CloneJob{}).Where("status = 'success'").Count(&stats.CloneJobStatusBreakdown.Success)
	db.Model(&models.CloneJob{}).Where("status = 'failed'").Count(&stats.CloneJobStatusBreakdown.Failed)
	db.Model(&models.CloneJob{}).Where("status = 'cancelled'").Count(&stats.CloneJobStatusBreakdown.Cancelled)

	// Repository status breakdown
	db.Model(&models.Repository{}).Where("status = 'pending'").Count(&stats.RepositoryStatusBreakdown.Pending)
	db.Model(&models.Repository{}).Where("status = 'cloning'").Count(&stats.RepositoryStatusBreakdown.Running)
	db.Model(&models.Repository{}).Where("status = 'success'").Count(&stats.RepositoryStatusBreakdown.Success)
	db.Model(&models.Repository{}).Where("status = 'failed'").Count(&stats.RepositoryStatusBreakdown.Failed)

	return stats, nil
}

// GetChartData returns data for dashboard charts.
func (s *DashboardService) GetChartData(ctx context.Context, days int) ([]ChartDay, error) {
	if days <= 0 {
		days = 30
	}

	startDate := time.Now().AddDate(0, 0, -days)

	// Get daily clone job counts using a simpler query.
	type DayStats struct {
		Day     time.Time
		Total   int64
		Success int64
		Failed  int64
	}
	var stats []DayStats

	err := s.db.WithContext(ctx).Model(&models.CloneJob{}).
		Select("DATE(created_at) as day, COUNT(*) as total, COUNT(*) FILTER (WHERE status = 'success') as success, COUNT(*) FILTER (WHERE status = 'failed') as failed").
		Where("created_at >= ?", startDate).
		Group("DATE(created_at)").
		Order("day ASC").
		Scan(&stats).Error
	if err != nil {
		return nil, err
	}

	chartData := make([]ChartDay, 0, len(stats))
	for _, stat := range stats {
		chartData = append(chartData, ChartDay{
			Date:    stat.Day,
			Total:   stat.Total,
			Success: stat.Success,
			Failed:  stat.Failed,
		})
	}

	return chartData, nil
}

// ChartDay represents a day in the chart data.
type ChartDay struct {
	Date    time.Time `json:"date"`
	Total   int64     `json:"total"`
	Success int64     `json:"success"`
	Failed  int64     `json:"failed"`
}

// GetTopRepositories returns the most active repositories.
func (s *DashboardService) GetTopRepositories(ctx context.Context, limit int) ([]models.Repository, error) {
	if limit <= 0 {
		limit = 10
	}

	var repos []models.Repository
	err := s.db.WithContext(ctx).
		Select("repositories.*, COUNT(clone_jobs.id) as clone_count").
		Joins("LEFT JOIN clone_jobs ON clone_jobs.repository_id = repositories.id").
		Group("repositories.id").
		Order("clone_count DESC").
		Limit(limit).
		Find(&repos).Error
	return repos, err
}

// GetTimelineData returns the recent clone jobs preloading their repositories.
func (s *DashboardService) GetTimelineData(ctx context.Context, limit int) ([]models.CloneJob, error) {
	if limit <= 0 {
		limit = 50
	}

	var jobs []models.CloneJob
	err := s.db.WithContext(ctx).
		Preload("Repository").
		Order("started_at DESC, created_at DESC").
		Limit(limit).
		Find(&jobs).Error
	return jobs, err
}

// GetPaperbinSize calculates the total storage size used by the paperbin and retrieves the configured quota limit.
func (s *DashboardService) GetPaperbinSize(ctx context.Context) (int64, int64, error) {
	var setting models.SystemSetting
	quotaGB := int64(50) // Default quota is 50 GB
	if err := s.db.WithContext(ctx).Where("key = ?", "paperbin_quota_gb").First(&setting).Error; err == nil {
		if val, parseErr := strconv.ParseInt(setting.Value, 10, 64); parseErr == nil && val > 0 {
			quotaGB = val
		}
	}

	var totalSize int64
	// Walk the repos base directory to sum up all files in any "paperbin" subdirectory
	_ = filepath.Walk("repos", func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		parts := strings.Split(filepath.ToSlash(path), "/")
		isPaperbin := false
		for _, part := range parts {
			if part == "paperbin" {
				isPaperbin = true
				break
			}
		}
		if isPaperbin && !info.IsDir() {
			totalSize += info.Size()
		}
		return nil
	})

	return totalSize, quotaGB, nil
}
