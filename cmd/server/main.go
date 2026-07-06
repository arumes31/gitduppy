package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/render"
	"github.com/gitduppy/gitduppy/internal/config"
	"github.com/gitduppy/gitduppy/internal/database"
	"github.com/gitduppy/gitduppy/internal/gitops"
	"github.com/gitduppy/gitduppy/internal/handlers"
	"github.com/gitduppy/gitduppy/internal/middleware"
	"github.com/gitduppy/gitduppy/internal/models"
	"github.com/gitduppy/gitduppy/internal/services"
	"github.com/gitduppy/gitduppy/pkg/crypto"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

var (
	Version   = "dev"
	BuildTime = "unknown"
)

func main() {
	log.Printf("Starting gitduppy %s (built: %s)", Version, BuildTime)

	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	// Normalize the storage base to an absolute path. It is baked into every
	// repository's StoragePath and resolved directly by browse/clone/cleanup; a
	// relative base would break the moment the process working directory differs.
	if abs, absErr := filepath.Abs(cfg.Storage.BasePath); absErr != nil {
		log.Fatalf("Failed to resolve storage base path %q: %v", cfg.Storage.BasePath, absErr)
	} else if abs != cfg.Storage.BasePath {
		log.Printf("Resolved storage base path %q -> %q", cfg.Storage.BasePath, abs)
		cfg.Storage.BasePath = abs
	}

	// Connect to database
	if err := database.Connect(&cfg.Database); err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer database.Close()

	// Run migrations
	if err := database.AutoMigrate(); err != nil {
		log.Fatalf("Failed to run migrations: %v", err)
	}

	// Normalize legacy repository storage paths to the current canonical form so
	// upgrades from builds that stored base-relative paths keep resolving on disk.
	if err := database.MigrateStoragePaths(cfg.Storage.BasePath); err != nil {
		log.Fatalf("Failed to migrate repository storage paths: %v", err)
	}

	// Create default admin user if none exists
	if err := createDefaultAdmin(); err != nil {
		log.Fatalf("Failed to create default admin user: %v", err)
	}

	// Initialize encryption service
	encryptionService, err := crypto.NewEncryptionService(cfg.Security.MasterKey)
	if err != nil {
		log.Fatalf("Failed to initialize encryption service: %v", err)
	}

	// Initialize services
	authService := services.NewAuthService(cfg.Security.SessionDuration)
	userService := services.NewUserService()
	repoService := services.NewRepositoryService(encryptionService, cfg.Storage.BasePath)
	cloneService := services.NewCloneService()
	apiKeyService := services.NewAPIKeyService()
	webhookService := services.NewWebhookService(cloneService, encryptionService)
	auditService := services.NewAuditService()
	tagService := services.NewTagService()
	dashboardService := services.NewDashboardService(cfg.Storage.BasePath)

	configService := services.NewConfigService(cfg, database.GetDB(), encryptionService)
	oauthService := services.NewOAuthService(configService)
	// Enable "mirror all my GitHub repos" on OAuth login.
	oauthService.SetRepositoryService(repoService)

	backupService := services.NewBackupService(cfg)
	emailService := services.NewEmailService(cfg)
	healthService := services.NewHealthService()

	// Initialize git operations
	gitOps := gitops.NewGitOperations(cfg.Storage.BasePath)

	// Initialize worker
	workerConfig := gitops.DefaultWorkerConfig()
	workerConfig.MaxConcurrent = cfg.Worker.MaxConcurrent
	workerConfig.CloneTimeout = cfg.Worker.CloneTimeout
	workerConfig.RetryMaxAttempts = cfg.Worker.RetryMaxAttempts
	workerConfig.RetryBaseDelay = cfg.Worker.RetryBaseDelay
	workerConfig.DedupeEnabled = cfg.Storage.DedupeEnabled

	cloneWorker := gitops.NewCloneWorker(workerConfig, gitOps, encryptionService)
	cloneWorker.SetNotificationServices(webhookService, emailService)
	// Dispatch newly created clone jobs straight to the worker pool.
	cloneService.SetEnqueuer(cloneWorker)
	cloneWorker.Start()
	defer cloneWorker.Stop()

	// Initialize scheduler
	scheduler := gitops.NewScheduler(cloneWorker)
	scheduler.Start()
	defer scheduler.Stop()

	// Refresh repository/storage Prometheus gauges on a schedule so scrapes see
	// current values regardless of dashboard traffic.
	if cfg.Monitoring.MetricsEnabled {
		stopMetrics := dashboardService.StartMetricsCollector(30 * time.Second)
		defer stopMetrics()
	}

	// Initialize cleanup worker
	cleanupWorker := gitops.NewCleanupWorker(gitops.DefaultCleanupConfig())
	cleanupWorker.Start()
	defer cleanupWorker.Stop()

	// Only honor forwarded headers (X-Forwarded-Proto for the Secure cookie flag)
	// when trusted proxies are configured; otherwise a direct client could spoof them.
	handlers.SetTrustProxyHeaders(len(cfg.Server.TrustedProxies) > 0)

	// Initialize handlers
	authHandler := handlers.NewAuthHandler(authService, auditService)
	userHandler := handlers.NewUserHandler(userService)
	repoHandler := handlers.NewRepositoryHandler(repoService, cloneService, tagService, auditService)
	cloneHandler := handlers.NewCloneHandler(cloneService)
	apiKeyHandler := handlers.NewAPIKeyHandler(apiKeyService)
	webhookHandler := handlers.NewWebhookHandler(webhookService)
	tagHandler := handlers.NewTagHandler(tagService)
	dashboardHandler := handlers.NewDashboardHandler(dashboardService, cloneService)
	healthHandler := handlers.NewHealthHandler(Version, BuildTime)
	healthHandler.SetQueueDepthProvider(cloneWorker.QueueDepth)
	oauthHandler := handlers.NewOAuthHandler(oauthService, authService)
	backupHandler := handlers.NewBackupHandler(backupService)
	configHandler := handlers.NewConfigHandler(configService)
	gitHealthHandler := handlers.NewGitHealthHandler(healthService)
	metricsHandler := handlers.NewMetricsHandler()
	webHandler := handlers.NewWebHandler()
	browseHandler := handlers.NewBrowseHandler(repoService)

	// Initialize middleware
	authMiddleware := middleware.NewAuthMiddleware()
	corsConfig := middleware.DefaultCORSConfig()
	// NewRateLimiter takes a refill rate in requests-per-SECOND, so convert the
	// configured per-minute budget; burst stays at one minute's worth of tokens.
	rateLimiter := middleware.NewRateLimiter(
		float64(cfg.Security.RateLimit.APIRequestsPerMinute)/60.0,
		cfg.Security.RateLimit.APIRequestsPerMinute,
	)
	defer rateLimiter.Stop()
	loggerConfig := middleware.DefaultLoggerConfig()
	csrfMiddleware := middleware.NewCSRFMiddleware(cfg.Security.CSRFKey, cfg.Server.TLS.Enabled)

	// Initialize Gin router
	router := setupRouter(
		cfg,
		authMiddleware,
		corsConfig,
		rateLimiter,
		loggerConfig,
		csrfMiddleware,
		authHandler,
		userHandler,
		repoHandler,
		cloneHandler,
		apiKeyHandler,
		webhookHandler,
		tagHandler,
		dashboardHandler,
		healthHandler,
		oauthHandler,
		backupHandler,
		configHandler,
		gitHealthHandler,
		metricsHandler,
		webHandler,
		browseHandler,
	)

	// Create HTTP server
	srv := &http.Server{
		Addr:         fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port),
		Handler:      router,
		ReadTimeout:  cfg.Server.ReadTimeout,
		WriteTimeout: cfg.Server.WriteTimeout,
		IdleTimeout:  cfg.Server.IdleTimeout,
	}

	// Start server in goroutine
	go func() {
		log.Printf("Server starting on %s:%d", cfg.Server.Host, cfg.Server.Port)
		if cfg.Server.TLS.Enabled {
			if err := srv.ListenAndServeTLS(cfg.Server.TLS.Cert, cfg.Server.TLS.Key); err != nil && err != http.ErrServerClosed {
				log.Fatalf("Server failed: %v", err)
			}
		} else {
			if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				log.Fatalf("Server failed: %v", err)
			}
		}
	}()

	// Wait for interrupt signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("Shutting down server...")

	// Graceful shutdown with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		log.Fatalf("Server forced to shutdown: %v", err)
	}

	log.Println("Server exited properly")
}

// generateBootstrapPassword returns a cryptographically random URL-safe password.
func generateBootstrapPassword(nBytes int) (string, error) {
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// createDefaultAdmin creates a default admin user if no users exist
func createDefaultAdmin() error {
	db := database.GetDB()
	var count int64
	if err := db.Model(&models.User{}).Count(&count).Error; err != nil {
		return fmt.Errorf("failed to count users: %w", err)
	}
	if count > 0 {
		if os.Getenv("GITMIRRORS_BOOTSTRAP_ADMIN_PASSWORD_RESET") == "true" {
			// Fail closed: every step of the forced reset must succeed, and the
			// admin row must actually be updated, before we log success or disclose
			// the password. Ignoring these errors could otherwise leave the admin
			// with an unknown/unchanged password while reporting a reset.
			bootstrapPassword := os.Getenv("GITMIRRORS_BOOTSTRAP_ADMIN_PASSWORD")
			if bootstrapPassword == "" {
				pw, genErr := generateBootstrapPassword(24)
				if genErr != nil {
					return fmt.Errorf("failed to generate bootstrap admin password: %w", genErr)
				}
				bootstrapPassword = pw
			}
			hashedPassword, err := bcrypt.GenerateFromPassword([]byte(bootstrapPassword), bcrypt.DefaultCost)
			if err != nil {
				return fmt.Errorf("failed to hash admin password for reset: %w", err)
			}
			passwordStr := string(hashedPassword)
			result := db.Model(&models.User{}).Where("username = ?", "admin").Update("password_hash", passwordStr)
			if result.Error != nil {
				return fmt.Errorf("failed to reset admin password: %w", result.Error)
			}
			if result.RowsAffected == 0 {
				return errors.New("admin password reset requested but no 'admin' user was updated")
			}
			if os.Getenv("GITMIRRORS_BOOTSTRAP_SHOW_PASSWORD") == "true" {
				log.Printf("=== ADMIN PASSWORD RESET (username: admin) — new password: %q ===", bootstrapPassword) //nolint:gosec // intentional for admin bootstrap
			} else {
				log.Println("Admin password forcefully reset from GITMIRRORS_BOOTSTRAP_ADMIN_PASSWORD.")
			}
		} else {
			log.Println("Users already exist, skipping default admin creation")
		}
		return nil
	}
	// Determine the initial admin password. Prefer an operator-supplied secret;
	// otherwise generate a strong random one (which is not logged by default).
	// This avoids baking a universal default password into every deployment.
	bootstrapPassword := os.Getenv("GITMIRRORS_BOOTSTRAP_ADMIN_PASSWORD")
	generated := false
	if bootstrapPassword == "" {
		pw, genErr := generateBootstrapPassword(24)
		if genErr != nil {
			return fmt.Errorf("failed to generate bootstrap admin password: %w", genErr)
		}
		bootstrapPassword = pw
		generated = true
	}

	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(bootstrapPassword), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("failed to hash password: %w", err)
	}
	passwordStr := string(hashedPassword)
	admin := &models.User{
		ID:           uuid.New(),
		Username:     "admin",
		Email:        "admin@gitmirrors.local",
		PasswordHash: &passwordStr,
		Role:         "admin",
		IsActive:     true,
	}
	if err := db.Create(admin).Error; err != nil {
		return fmt.Errorf("failed to create admin user: %w", err)
	}
	switch {
	case !generated:
		log.Println("Default admin user created (username: admin) from GITMIRRORS_BOOTSTRAP_ADMIN_PASSWORD - change on first login")
	case os.Getenv("GITMIRRORS_BOOTSTRAP_SHOW_PASSWORD") == "true":
		// CodeQL [go/clear-text-logging] - Explicit operator opt-in to print the one-time generated bootstrap password.
		// #nosec G706 - Explicit operator opt-in to print the password to logs.
		log.Printf("=== INITIAL ADMIN CREATED (username: admin) — one-time generated password: %s — change it immediately after first login ===", bootstrapPassword)
	default:
		// Never log the generated secret by default. Direct the operator to
		// provide a password or opt in to a one-time display.
		log.Println("Initial admin user 'admin' created with a random password that was NOT logged. " +
			"Set GITMIRRORS_BOOTSTRAP_ADMIN_PASSWORD before first start to choose it, " +
			"or set GITMIRRORS_BOOTSTRAP_SHOW_PASSWORD=true to print the generated one once at startup.")
	}
	defaultTags := []models.Tag{
		{Name: "production", Color: "#ef4444"},
		{Name: "staging", Color: "#f59e0b"},
		{Name: "development", Color: "#22c55e"},
		{Name: "archived", Color: "#6b7280"},
	}
	for _, tag := range defaultTags {
		var existing models.Tag
		if err := db.Where("name = ?", tag.Name).First(&existing).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				tag.ID = uuid.New()
				if err := db.Create(&tag).Error; err != nil {
					return fmt.Errorf("failed to create default tag %s: %w", tag.Name, err)
				}
			} else {
				return fmt.Errorf("failed to query default tag %s: %w", tag.Name, err)
			}
		}
	}
	log.Println("Default tags created")
	return nil
}

// setupRouter configures the Gin router with middleware and routes
func setupRouter(
	cfg *config.Config,
	authMiddleware *middleware.AuthMiddleware,
	corsConfig *middleware.CORSConfig,
	rateLimiter *middleware.RateLimiter,
	loggerConfig *middleware.LoggerConfig,
	csrfMiddleware *middleware.CSRFMiddleware,
	authHandler *handlers.AuthHandler,
	userHandler *handlers.UserHandler,
	repoHandler *handlers.RepositoryHandler,
	cloneHandler *handlers.CloneHandler,
	apiKeyHandler *handlers.APIKeyHandler,
	webhookHandler *handlers.WebhookHandler,
	tagHandler *handlers.TagHandler,
	dashboardHandler *handlers.DashboardHandler,
	healthHandler *handlers.HealthHandler,
	oauthHandler *handlers.OAuthHandler,
	backupHandler *handlers.BackupHandler,
	configHandler *handlers.ConfigHandler,
	gitHealthHandler *handlers.GitHealthHandler,
	metricsHandler *handlers.MetricsHandler,
	webHandler *handlers.WebHandler,
	browseHandler *handlers.BrowseHandler,
) *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()

	// Static file serving and HTML Templates
	router.Static("/static", "./static")
	router.Static("/assets", "./internal/web/static")
	router.HTMLRender = loadTemplates()

	// Middleware - add panic recovery
	router.Use(gin.Recovery())
	router.Use(middleware.GinLogger())
	router.Use(middleware.CORS(corsConfig))
	router.Use(rateLimiter.Middleware())
	router.Use(middleware.SecurityHeaders(cfg))
	if cfg.Monitoring.MetricsEnabled {
		router.Use(middleware.Metrics())
	}

	// Prometheus metrics endpoint (no auth required)
	if cfg.Monitoring.MetricsEnabled {
		router.GET(cfg.Monitoring.MetricsPath, metricsHandler.GetMetrics)
	}

	// Web UI Routes
	router.GET("/login", webHandler.Login)
	router.GET("/", webHandler.Index)

	webGroup := router.Group("/")
	webGroup.Use(authMiddleware.WebMiddleware())
	{
		webGroup.GET("/dashboard", webHandler.Dashboard)
		webGroup.GET("/config", webHandler.Config)
		webGroup.GET("/repos", webHandler.RepoList)
		webGroup.GET("/repos/:id", webHandler.RepoDetail)
		webGroup.GET("/repos/:id/commit/:sha", webHandler.RepoCommit)
		webGroup.GET("/search", webHandler.Search)
	}

	// Health check endpoints (no auth required). They accept GET and HEAD because
	// container healthchecks use `wget --spider` (a HEAD request), and Gin does not
	// route HEAD to a GET-only handler (it would 404).
	healthMethods := []string{http.MethodGet, http.MethodHead}
	router.Match(healthMethods, "/api/v1/health", healthHandler.GetHealth)
	router.Match(healthMethods, "/api/v1/health/live", healthHandler.GetHealthLive)
	router.Match(healthMethods, "/api/v1/health/ready", healthHandler.GetHealthReady)
	router.Match(healthMethods, "/api/v1/health/git-servers", gitHealthHandler.GetGitServerHealth)

	// API v1 group
	v1 := router.Group("/api/v1")
	{
		// Browse routes (authenticated)
		browseGroup := v1.Group("/repos")
		browseGroup.Use(authMiddleware.Middleware())
		{
			browseGroup.GET("/:id/refs", browseHandler.GetRefs)
			browseGroup.GET("/:id/tree", browseHandler.GetTree)
			browseGroup.GET("/:id/blob", browseHandler.GetBlob)
			browseGroup.GET("/:id/commits", browseHandler.GetCommits)
			browseGroup.GET("/:id/commit/:sha", browseHandler.GetCommit)
			browseGroup.GET("/:id/download", browseHandler.DownloadRepo)
		}
		// Auth routes
		auth := v1.Group("/auth")
		{
			auth.POST("/login", authHandler.Login)
			auth.POST("/logout", authHandler.Logout)
			auth.GET("/me", authMiddleware.Middleware(), authHandler.Me)
			auth.POST("/refresh", authHandler.Refresh)
			auth.POST("/change-password", authMiddleware.Middleware(), authHandler.ChangePassword)
		}

		// OAuth routes
		oauth := v1.Group("/oauth")
		{
			oauth.POST("/github/manifest-setup", authMiddleware.Middleware(), middleware.RequireAdmin(), oauthHandler.ManifestSetup)
			oauth.GET("/github/manifest-callback", oauthHandler.ManifestCallback)
			oauth.GET("/:provider/login", oauthHandler.LoginWithProvider)
			oauth.GET("/:provider/callback", oauthHandler.Callback)
			oauth.POST("/:provider/link", authMiddleware.Middleware(), oauthHandler.LinkAccount)
			oauth.GET("/:provider/link/callback", authMiddleware.Middleware(), oauthHandler.LinkCallback)
			oauth.DELETE("/:provider/unlink", authMiddleware.Middleware(), oauthHandler.UnlinkAccount)
		}

		// User routes
		users := v1.Group("/users")
		users.Use(authMiddleware.Middleware())
		{
			users.GET("", middleware.RequireAdmin(), userHandler.ListUsers)
			users.POST("", middleware.RequireAdmin(), userHandler.CreateUser)
			users.GET("/me", userHandler.GetCurrentUser)
			users.GET("/:id", middleware.RequireAdmin(), userHandler.GetUser)
			users.PUT("/:id", middleware.RequireAdmin(), userHandler.UpdateUser)
			users.DELETE("/:id", middleware.RequireAdmin(), userHandler.DeleteUser)
			users.PATCH("/:id/status", middleware.RequireAdmin(), userHandler.SetUserStatus)
		}

		// Global search route
		v1.GET("/search", authMiddleware.Middleware(), repoHandler.GlobalSearch)

		// Repository routes
		repos := v1.Group("/repositories")
		repos.Use(authMiddleware.Middleware())
		{
			repos.GET("", repoHandler.ListRepositories)
			repos.POST("", repoHandler.CreateRepository)
			repos.GET("/paperbin", repoHandler.GetPaperbin)
			repos.GET("/:id", repoHandler.GetRepository)
			repos.PUT("/:id", repoHandler.UpdateRepository)
			repos.DELETE("/:id", repoHandler.DeleteRepository)
			repos.POST("/:id/restore", repoHandler.RestoreRepository)
			repos.DELETE("/:id/force", repoHandler.PermanentDeleteRepository)
			repos.POST("/:id/paperbin/branches/:branchId/restore", repoHandler.RestoreBranch)
			repos.DELETE("/:id/paperbin/branches/:branchId", repoHandler.PermanentDeleteBranch)
			repos.PATCH("/:id/status", repoHandler.SetRepositoryStatus)
			repos.POST("/:id/clone", repoHandler.TriggerClone)
			repos.GET("/:id/logs", repoHandler.GetRepositoryLogs)
			repos.GET("/:id/logs/stream", repoHandler.StreamRepositoryLogs)
			repos.GET("/:id/jobs", cloneHandler.ListRepositoryJobs)
		}

		// Clone job routes
		jobs := v1.Group("/clone-jobs")
		jobs.Use(authMiddleware.Middleware())
		{
			jobs.GET("", cloneHandler.ListCloneJobs)
			jobs.GET("/:id", cloneHandler.GetCloneJob)
			jobs.POST("/:id/cancel", cloneHandler.CancelCloneJob)
			jobs.GET("/:id/logs", cloneHandler.GetCloneJobLogs)
		}

		// API Key routes
		apiKeys := v1.Group("/api-keys")
		apiKeys.Use(authMiddleware.Middleware())
		{
			apiKeys.GET("", apiKeyHandler.ListAPIKeys)
			apiKeys.POST("", apiKeyHandler.CreateAPIKey)
			apiKeys.DELETE("/:id", apiKeyHandler.DeleteAPIKey)
			apiKeys.POST("/:id/revoke", apiKeyHandler.RevokeAPIKey)
		}

		// Webhook routes
		webhooks := v1.Group("/webhooks")
		webhooks.Use(authMiddleware.Middleware())
		{
			webhooks.GET("", webhookHandler.ListWebhooks)
			webhooks.POST("", webhookHandler.CreateWebhook)
			webhooks.GET("/:id", webhookHandler.GetWebhook)
			webhooks.PUT("/:id", webhookHandler.UpdateWebhook)
			webhooks.DELETE("/:id", webhookHandler.DeleteWebhook)
			webhooks.GET("/:id/deliveries", webhookHandler.GetWebhookDeliveries)
			webhooks.POST("/:id/test", webhookHandler.TestWebhook)
		}

		// Tag routes
		tags := v1.Group("/tags")
		tags.Use(authMiddleware.Middleware())
		{
			tags.GET("", tagHandler.ListTags)
			tags.POST("", middleware.RequireAdmin(), tagHandler.CreateTag)
			tags.GET("/:id", tagHandler.GetTag)
			tags.PUT("/:id", middleware.RequireAdmin(), tagHandler.UpdateTag)
			tags.DELETE("/:id", middleware.RequireAdmin(), tagHandler.DeleteTag)
		}

		// Repository tag routes
		repoTags := v1.Group("/repositories/:id/tags")
		repoTags.Use(authMiddleware.Middleware())
		{
			repoTags.GET("", tagHandler.GetRepositoryTags)
			repoTags.POST("", tagHandler.AddTagToRepository)
			repoTags.DELETE("/:tagId", tagHandler.RemoveTagFromRepository)
			repoTags.PUT("", tagHandler.SetRepositoryTags)
		}

		// Dashboard routes
		dashboard := v1.Group("/dashboard")
		dashboard.Use(authMiddleware.Middleware())
		{
			dashboard.GET("/stats", dashboardHandler.GetStats)
			dashboard.GET("/chart-data", dashboardHandler.GetChartData)
			dashboard.GET("/top-repositories", dashboardHandler.GetTopRepositories)
			dashboard.GET("/recent-jobs", dashboardHandler.GetRecentJobs)
			dashboard.GET("/timeline", dashboardHandler.GetTimeline)
			dashboard.GET("/paperbin-quota", dashboardHandler.GetPaperbinQuota)
		}

		// Backup routes
		backup := v1.Group("/backup")
		backup.Use(authMiddleware.Middleware())
		{
			backup.GET("/export", backupHandler.Export)
			backup.POST("/import", backupHandler.Import)
		}

		// Config routes
		configRoutes := v1.Group("/config")
		configRoutes.Use(authMiddleware.Middleware())
		{
			configRoutes.GET("", configHandler.GetConfig)
			configRoutes.PUT("", middleware.RequireAdmin(), configHandler.UpdateConfig)
			configRoutes.PUT("/oauth", middleware.RequireAdmin(), configHandler.UpdateOAuthSettings)
			configRoutes.GET("/maintenance", configHandler.GetMaintenanceMode)
			configRoutes.PUT("/maintenance", middleware.RequireAdmin(), configHandler.UpdateMaintenanceMode)
			configRoutes.GET("/quota", configHandler.GetQuota)
			configRoutes.PUT("/quota", middleware.RequireAdmin(), configHandler.UpdateQuota)
		}

		// Incoming webhook receiver (no auth required, uses HMAC signature)
		v1.POST("/webhooks/receive", webhookHandler.ReceiveWebhook)
	}

	return router
}

// CustomHTMLRenderer is a custom HTML renderer for Gin that prevents template block collisions.
type CustomHTMLRenderer map[string]*template.Template

func (r CustomHTMLRenderer) Instance(name string, data interface{}) render.Render {
	return render.HTML{
		Template: r[name],
		Name:     name,
		Data:     data,
	}
}

func loadTemplates() CustomHTMLRenderer {
	r := make(CustomHTMLRenderer)
	r["login.html"] = template.Must(template.ParseFiles("internal/web/templates/base.html", "internal/web/templates/login.html"))
	r["dashboard.html"] = template.Must(template.ParseFiles("internal/web/templates/base.html", "internal/web/templates/dashboard.html"))
	r["config.html"] = template.Must(template.ParseFiles("internal/web/templates/base.html", "internal/web/templates/config.html"))
	r["repos.html"] = template.Must(template.ParseFiles("internal/web/templates/base.html", "internal/web/templates/repos.html"))
	r["repo_detail.html"] = template.Must(template.ParseFiles("internal/web/templates/base.html", "internal/web/templates/repo_detail.html"))
	r["repo_commit.html"] = template.Must(template.ParseFiles("internal/web/templates/base.html", "internal/web/templates/repo_commit.html"))
	r["search.html"] = template.Must(template.ParseFiles("internal/web/templates/base.html", "internal/web/templates/search.html"))
	return r
}
