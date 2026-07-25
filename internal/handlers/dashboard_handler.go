package handlers

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gitduppy/gitduppy/internal/gitops"
	"github.com/gitduppy/gitduppy/internal/models"
	"github.com/gitduppy/gitduppy/internal/services"
	"github.com/gitduppy/gitduppy/pkg/response"
	"github.com/gorilla/websocket"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
	"gorm.io/gorm"
)

// DashboardHandler handles dashboard requests.
type DashboardHandler struct {
	dashboardService *services.DashboardService
	cloneService     *services.CloneService
	// db backs the dedupe-savings widget's active-repository query. Injected
	// explicitly (rather than reached for via database.GetDB() inside handler
	// methods) for the same testability reason as RepositoryHandler.
	db *gorm.DB

	gitOps          *gitops.GitOperations
	queueDepthFn    func() int
	maxConcurrentFn func() int
}

// NewDashboardHandler creates a new dashboard handler.
func NewDashboardHandler(dashboardService *services.DashboardService, cloneService *services.CloneService, db *gorm.DB) *DashboardHandler {
	return &DashboardHandler{
		dashboardService: dashboardService,
		cloneService:     cloneService,
		db:               db,
	}
}

// SetWorkerProviders wires the clone worker's live queue-depth and pool-size
// accessors so GetOverview can report queue pressure without the handler
// depending on the full worker type.
func (h *DashboardHandler) SetWorkerProviders(queueDepthFn, maxConcurrentFn func() int) {
	h.queueDepthFn = queueDepthFn
	h.maxConcurrentFn = maxConcurrentFn
}

// SetGitOps wires the git operations instance used for the dedupe-savings
// widget (it owns the storage base path and pool-path hashing).
func (h *DashboardHandler) SetGitOps(gitOps *gitops.GitOperations) {
	h.gitOps = gitOps
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

// GetOverview handles GET /api/v1/dashboard/overview. It returns the
// dashboard's independent read payloads — stats, the 5 most recent jobs, the
// timeline, the paperbin quota, the clone queue status, upcoming scheduled
// syncs, recent failures, the GitHub API rate limit, and dedupe-savings — in a
// single response so the client makes one round trip instead of many. The
// fetches run concurrently via errgroup; the original single-purpose
// endpoints are left untouched for backward compatibility.
//
// Partial failure fails the whole request (respondServiceError): the payload is
// rendered as a unit, so returning most sections would be misleading, and a
// single error code is simpler for the caller than per-section error handling.
// The two provider-backed fields (rate limit, dedupe savings) are the
// exception — they are "not yet observed" rather than erroring, since neither
// worker provider nor gitOps may be wired in every deployment (e.g. tests).
func (h *DashboardHandler) GetOverview(c *gin.Context) {
	const (
		recentJobsLimit     = 5
		timelineLimit       = 50
		nextSyncsLimit      = 5
		recentFailuresLimit = 5
	)

	var (
		stats          *services.DashboardStats
		jobs           []models.CloneJob
		timeline       []models.CloneJob
		pbSize         int64
		pbQuotaGB      float64
		runningCount   int64
		nextSyncs      []services.NextSync
		recentFailures []models.CloneJob
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
	g.Go(func() error {
		var err error
		runningCount, err = h.dashboardService.GetRunningJobCount(ctx)
		return err
	})
	g.Go(func() error {
		var err error
		nextSyncs, err = h.dashboardService.GetNextSyncs(ctx, nextSyncsLimit)
		return err
	})
	g.Go(func() error {
		var err error
		recentFailures, err = h.dashboardService.GetRecentFailures(ctx, recentFailuresLimit)
		return err
	})

	if err := g.Wait(); err != nil {
		respondServiceError(c, err)
		return
	}

	quotaBytes := int64(pbQuotaGB * 1024 * 1024 * 1024)

	var queueDepthVal any
	if h.queueDepthFn != nil {
		queueDepthVal = h.queueDepthFn()
	}
	var maxConcurrentVal any
	if h.maxConcurrentFn != nil {
		maxConcurrentVal = h.maxConcurrentFn()
	}
	queuePayload := gin.H{
		"depth":          queueDepthVal,
		"running":        runningCount,
		"max_concurrent": maxConcurrentVal,
	}

	var rateLimitPayload any
	if remaining, limit, resetAt, observed := gitops.GetGitHubRateLimit(); observed {
		rateLimitPayload = gin.H{
			"remaining": remaining,
			"limit":     limit,
			"reset_at":  resetAt,
		}
	}

	var dedupePayload any
	if h.gitOps != nil {
		if savings := h.gitOps.DedupeSavings(h.db); savings != nil {
			dedupePayload = savings
		}
	}

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
		"queue":             queuePayload,
		"next_syncs":        nextSyncs,
		"recent_failures":   recentFailures,
		"github_rate_limit": rateLimitPayload,
		"dedupe_savings":    dedupePayload,
	})
}

// firehoseFrame is the JSON envelope for one dashboard firehose line. Type is
// a discriminator mirroring logFrame's, so future frame kinds can share the
// socket without a breaking format change.
type firehoseFrame struct {
	Type           string `json:"type"`
	TS             string `json:"ts"`
	RepositoryID   string `json:"repository_id"`
	RepositoryName string `json:"repository_name"`
	Line           string `json:"line"`
}

// StreamDashboardLogs upgrades the request to a websocket and streams a
// combined feed of live progress logs from every currently-running clone job,
// each line tagged with which repository it came from. It mirrors
// RepositoryHandler.StreamRepositoryLogs (same origin-check upgrader,
// ping-keepalive, and peer-disconnect handling) but subscribes to the
// firehose instead of a single repository's channel.
func (h *DashboardHandler) StreamDashboardLogs(c *gin.Context) {
	ws, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		zap.L().Named("ws").Debug("WebSocket upgrade failed", zap.Error(err))
		return
	}
	defer ws.Close()

	logChan := gitops.GlobalLogHub.SubscribeAll()
	defer gitops.GlobalLogHub.UnsubscribeAll(logChan)

	// See StreamRepositoryLogs for why this reader goroutine exists: a
	// hijacked websocket's request context does not observe the peer closing
	// the connection on its own.
	ctx, cancel := context.WithCancel(c.Request.Context())
	defer cancel()
	go func() {
		for {
			if _, _, rerr := ws.ReadMessage(); rerr != nil {
				cancel()
				return
			}
		}
	}()

	var writeMu sync.Mutex
	writeMessage := func(messageType int, data []byte) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		return ws.WriteMessage(messageType, data)
	}

	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if err := writeMessage(websocket.PingMessage, []byte{}); err != nil {
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	for {
		select {
		case entry, ok := <-logChan:
			if !ok {
				return
			}
			payload, err := json.Marshal(firehoseFrame{
				Type:           "log",
				TS:             entry.TS,
				RepositoryID:   entry.RepositoryID,
				RepositoryName: entry.RepositoryName,
				Line:           entry.Line,
			})
			if err != nil {
				// A handful of string fields can't realistically fail to marshal;
				// skip this line rather than tear down the stream if it somehow does.
				continue
			}
			if err := writeMessage(websocket.TextMessage, payload); err != nil {
				return
			}
		case <-ctx.Done():
			return
		}
	}
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
