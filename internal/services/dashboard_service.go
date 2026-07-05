package services

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gitduppy/gitduppy/internal/database"
	"github.com/gitduppy/gitduppy/internal/metrics"
	"github.com/gitduppy/gitduppy/internal/models"
	"gorm.io/gorm"
)

// storageSizeTTL is how long a computed on-disk storage total is reused before
// the tree is walked again. Dashboard stats are polled frequently, and walking a
// large mirror on every request is expensive, so the value is memoized.
const storageSizeTTL = 60 * time.Second

// DashboardService handles dashboard statistics.
type DashboardService struct {
	db       *gorm.DB
	basePath string

	storageMu         sync.Mutex
	storageBytes      int64
	paperbinBytes     int64
	storageAt         time.Time
	storageRefreshing bool

	statsMu     sync.Mutex
	cachedStats *DashboardStats
	statsAt     time.Time
}

// statsCacheTTL is how long a computed dashboard-stats snapshot is reused. The
// dashboard polls frequently; a few seconds of staleness is imperceptible and
// spares the DB a dozen aggregate queries per poll.
const statsCacheTTL = 5 * time.Second

// NewDashboardService creates a new dashboard service. basePath is the storage
// root used to compute total on-disk usage.
func NewDashboardService(basePath string) *DashboardService {
	return &DashboardService{
		db:       database.GetDB(),
		basePath: basePath,
	}
}

// totalStorageBytes returns the on-disk size of the storage tree without ever
// blocking the request: it returns the last cached value immediately and, when
// that value is stale (or unset), kicks off a single background walk to refresh
// it. The expensive filepath.Walk never runs while storageMu is held, and the
// request never waits on it, so a cancelled request context is moot here.
// The first dashboard load reports 0 until the initial background walk lands.
func (s *DashboardService) totalStorageBytes() int64 {
	if s.basePath == "" {
		return 0
	}
	s.storageMu.Lock()
	cached := s.storageBytes
	stale := s.storageAt.IsZero() || time.Since(s.storageAt) >= storageSizeTTL
	if stale && !s.storageRefreshing {
		s.storageRefreshing = true
		go s.refreshStorage()
	}
	s.storageMu.Unlock()
	return cached
}

// refreshStorage walks the storage tree once off the request path and updates
// both the cached total and the paperbin subtotal. Only one refresh runs at a
// time (guarded by storageRefreshing), and storageMu is not held during the walk.
func (s *DashboardService) refreshStorage() {
	total, paperbin := scanStorage(s.basePath)
	s.storageMu.Lock()
	s.storageBytes = total
	s.paperbinBytes = paperbin
	s.storageAt = time.Now()
	s.storageRefreshing = false
	s.storageMu.Unlock()
}

// paperbinSizeBytes returns the cached on-disk size of paperbin contents without
// blocking the request, kicking off the shared background storage refresh when
// the cached value is stale. Like totalStorageBytes it reports 0 until the first
// background walk lands.
func (s *DashboardService) paperbinSizeBytes() int64 {
	if s.basePath == "" {
		return 0
	}
	s.storageMu.Lock()
	cached := s.paperbinBytes
	stale := s.storageAt.IsZero() || time.Since(s.storageAt) >= storageSizeTTL
	if stale && !s.storageRefreshing {
		s.storageRefreshing = true
		go s.refreshStorage()
	}
	s.storageMu.Unlock()
	return cached
}

// scanStorage walks root once, returning the total size in bytes of all files
// and the subtotal of files that live under any "paperbin" subdirectory. Missing
// or unreadable entries contribute zero rather than erroring so the stat is
// best-effort. Uses WalkDir to avoid a stat() syscall per entry.
func scanStorage(root string) (total, paperbin int64) {
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d == nil {
			return nil //nolint:nilerr // best-effort: skip unreadable entries
		}
		if d.IsDir() {
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil {
			return nil
		}
		total += info.Size()
		for _, part := range strings.Split(filepath.ToSlash(path), "/") {
			if part == "paperbin" {
				paperbin += info.Size()
				break
			}
		}
		return nil
	})
	return total, paperbin
}

// dirSize returns the total size in bytes of all files under root. Missing paths
// contribute zero rather than erroring so the stat is best-effort.
func dirSize(root string) int64 {
	total, _ := scanStorage(root)
	return total
}

// DashboardStats represents dashboard statistics.
type DashboardStats struct {
	TotalRepositories         int64            `json:"total_repositories"`
	ActiveRepositories        int64            `json:"active_repositories"`
	FailedRepositories        int64            `json:"failed_repositories"`
	TotalCloneJobs            int64            `json:"total_clone_jobs"`
	SuccessfulClones          int64            `json:"successful_clones"`
	FailedClones              int64            `json:"failed_clones"`
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

// GetStats returns dashboard statistics, cached for statsCacheTTL to absorb
// frequent polling.
func (s *DashboardService) GetStats(ctx context.Context) (*DashboardStats, error) {
	s.statsMu.Lock()
	if s.cachedStats != nil && time.Since(s.statsAt) < statsCacheTTL {
		cached := s.cachedStats
		s.statsMu.Unlock()
		return cached, nil
	}
	s.statsMu.Unlock()

	stats := &DashboardStats{
		RecentActivity:            &RecentActivity{},
		CloneJobStatusBreakdown:   &StatusBreakdown{},
		RepositoryStatusBreakdown: &StatusBreakdown{},
	}

	db := s.db.WithContext(ctx)

	// Repository counts + status breakdown in one grouped scan instead of five
	// separate COUNT queries.
	type repoStatusRow struct {
		IsActive bool
		Status   string
		Count    int64
	}
	var repoRows []repoStatusRow
	if err := db.Model(&models.Repository{}).Select("is_active, status, COUNT(*) as count").Group("is_active, status").Scan(&repoRows).Error; err != nil {
		// Don't cache all-zero stats from a failed aggregation; surface the error.
		return nil, err
	}
	for _, r := range repoRows {
		if r.IsActive {
			stats.TotalRepositories += r.Count
			switch r.Status {
			case "success":
				stats.ActiveRepositories += r.Count
			case "failed":
				stats.FailedRepositories += r.Count
			}
		}
		switch r.Status {
		case "pending":
			stats.RepositoryStatusBreakdown.Pending += r.Count
		case "cloning":
			stats.RepositoryStatusBreakdown.Running += r.Count
		case "success":
			stats.RepositoryStatusBreakdown.Success += r.Count
		case "failed":
			stats.RepositoryStatusBreakdown.Failed += r.Count
		}
	}

	// Clone job counts + status breakdown + success/fail totals in one grouped
	// scan instead of eight separate COUNT queries.
	type jobStatusRow struct {
		Status string
		Count  int64
	}
	var jobRows []jobStatusRow
	if err := db.Model(&models.CloneJob{}).Select("status, COUNT(*) as count").Group("status").Scan(&jobRows).Error; err != nil {
		return nil, err
	}
	for _, r := range jobRows {
		stats.TotalCloneJobs += r.Count
		switch r.Status {
		case "pending":
			stats.CloneJobStatusBreakdown.Pending += r.Count
		case "running":
			stats.CloneJobStatusBreakdown.Running += r.Count
		case "success":
			stats.CloneJobStatusBreakdown.Success += r.Count
			stats.SuccessfulClones += r.Count
		case "failed":
			stats.CloneJobStatusBreakdown.Failed += r.Count
			stats.FailedClones += r.Count
		case "cancelled":
			stats.CloneJobStatusBreakdown.Cancelled += r.Count
		}
	}
	if completed := stats.SuccessfulClones + stats.FailedClones; completed > 0 {
		stats.SuccessRate = float64(stats.SuccessfulClones) / float64(completed) * 100
	}

	// Total on-disk storage used by mirrored repositories (best-effort, cached
	// walk — see totalStorageBytes).
	stats.TotalStorageBytes = s.totalStorageBytes()

	// Average clone duration
	type DurationResult struct {
		AvgDuration float64
	}
	var durationResult DurationResult
	db.Model(&models.CloneJob{}).
		Select("AVG(EXTRACT(EPOCH FROM (completed_at - started_at))) as avg_duration").
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

	// Status breakdowns were computed above in the grouped scans.

	s.statsMu.Lock()
	s.cachedStats = stats
	s.statsAt = time.Now()
	s.statsMu.Unlock()

	return stats, nil
}

// StartMetricsCollector periodically refreshes the repository-status and storage
// Prometheus gauges independently of request traffic, so a Prometheus scrape sees
// current values even when nobody is viewing the dashboard. It returns a stop
// function that halts the background goroutine. An initial refresh runs
// synchronously so the gauges are populated before the first scrape.
func (s *DashboardService) StartMetricsCollector(interval time.Duration) (stop func()) {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	done := make(chan struct{})
	s.refreshGauges(context.Background())
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				s.refreshGauges(context.Background())
			case <-done:
				return
			}
		}
	}()
	return func() { close(done) }
}

// refreshGauges recomputes the repository-status counts and on-disk storage total
// and publishes them to the Prometheus gauges.
func (s *DashboardService) refreshGauges(ctx context.Context) {
	type repoStatusRow struct {
		Status string
		Count  int64
	}
	var rows []repoStatusRow
	if err := s.db.WithContext(ctx).Model(&models.Repository{}).
		Select("status, COUNT(*) as count").Group("status").Scan(&rows).Error; err == nil {
		counts := map[string]int64{"pending": 0, "cloning": 0, "success": 0, "failed": 0}
		for _, r := range rows {
			if _, ok := counts[r.Status]; ok {
				counts[r.Status] = r.Count
			}
		}
		for status, n := range counts {
			metrics.RepositoriesTotal.WithLabelValues(status).Set(float64(n))
		}
	}
	metrics.StorageBytes.Set(float64(s.totalStorageBytes()))
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
	// Order by effective activity time: pending jobs have NULL started_at (which
	// Postgres would otherwise sort first), so fall back to created_at.
	err := s.db.WithContext(ctx).
		Preload("Repository").
		Order("COALESCE(started_at, created_at) DESC").
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

	// Paperbin size comes from the shared, memoized storage scan (see
	// paperbinSizeBytes / refreshStorage) rather than a per-request tree walk,
	// keeping the expensive traversal off the hot path.
	return s.paperbinSizeBytes(), quotaGB, nil
}
