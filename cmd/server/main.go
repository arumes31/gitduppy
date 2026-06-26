package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
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

	// Connect to database
	if err := database.Connect(&cfg.Database); err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer database.Close()

	// Run migrations
	if err := database.AutoMigrate(); err != nil {
		log.Fatalf("Failed to run migrations: %v", err)
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
	repoService := services.NewRepositoryService(encryptionService)
	cloneService := services.NewCloneService()
	apiKeyService := services.NewAPIKeyService()
	webhookService := services.NewWebhookService(cloneService)
	auditService := services.NewAuditService()
	tagService := services.NewTagService()
	dashboardService := services.NewDashboardService()

	configService := services.NewConfigService(cfg, database.GetDB(), encryptionService)
	oauthService := services.NewOAuthService(configService)

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

	cloneWorker := gitops.NewCloneWorker(workerConfig, gitOps, encryptionService)
	cloneWorker.SetNotificationServices(webhookService, emailService)
	cloneWorker.Start()
	defer cloneWorker.Stop()

	// Initialize scheduler
	scheduler := gitops.NewScheduler(cloneWorker)
	scheduler.Start()
	defer scheduler.Stop()

	// Initialize cleanup worker
	cleanupWorker := gitops.NewCleanupWorker(gitops.DefaultCleanupConfig())
	cleanupWorker.Start()
	defer cleanupWorker.Stop()

	// Initialize handlers
	authHandler := handlers.NewAuthHandler(authService)
	userHandler := handlers.NewUserHandler(userService)
	repoHandler := handlers.NewRepositoryHandler(repoService, cloneService, tagService, auditService)
	cloneHandler := handlers.NewCloneHandler(cloneService)
	apiKeyHandler := handlers.NewAPIKeyHandler(apiKeyService)
	webhookHandler := handlers.NewWebhookHandler(webhookService)
	tagHandler := handlers.NewTagHandler(tagService)
	dashboardHandler := handlers.NewDashboardHandler(dashboardService, cloneService)
	healthHandler := handlers.NewHealthHandler(Version, BuildTime)
	oauthHandler := handlers.NewOAuthHandler(oauthService, authService)
	backupHandler := handlers.NewBackupHandler(backupService)
	configHandler := handlers.NewConfigHandler(configService)
	gitHealthHandler := handlers.NewGitHealthHandler(healthService)
	metricsHandler := handlers.NewMetricsHandler()
	webHandler := handlers.NewWebHandler()

	// Initialize middleware
	authMiddleware := middleware.NewAuthMiddleware()
	corsConfig := middleware.DefaultCORSConfig()
	rateLimiter := middleware.NewRateLimiter(
		float64(cfg.Security.RateLimit.APIRequestsPerMinute),
		cfg.Security.RateLimit.APIRequestsPerMinute,
	)
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

// createDefaultAdmin creates a default admin user if no users exist
func createDefaultAdmin() error {
	db := database.GetDB()
	var count int64
	if err := db.Model(&models.User{}).Count(&count).Error; err != nil {
		return fmt.Errorf("failed to count users: %w", err)
	}
	if count > 0 {
		log.Println("Users already exist, skipping default admin creation")
		return nil
	}
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte("admin123"), bcrypt.DefaultCost)
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
	log.Println("Default admin user created (username: admin, password: admin123 - change on first login)")
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
) *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()

	// Static file serving and HTML Templates
	router.Static("/static", "./static")
	router.Static("/assets", "./internal/web/static")
	router.LoadHTMLGlob("internal/web/templates/*")

	// Middleware - add panic recovery
	router.Use(gin.Recovery())
	router.Use(middleware.GinLogger())
	router.Use(middleware.CORS(corsConfig))
	router.Use(rateLimiter.Middleware())
	router.Use(middleware.SecurityHeaders(cfg))

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
	}

	// Health check endpoints (no auth required)
	router.GET("/api/v1/health", healthHandler.GetHealth)
	router.GET("/api/v1/health/live", healthHandler.GetHealthLive)
	router.GET("/api/v1/health/ready", healthHandler.GetHealthReady)
	router.GET("/api/v1/health/git-servers", gitHealthHandler.GetGitServerHealth)

	// API v1 group
	v1 := router.Group("/api/v1")
	{
		// Auth routes
		auth := v1.Group("/auth")
		{
			auth.POST("/login", authHandler.Login)
			auth.POST("/logout", authHandler.Logout)
			auth.GET("/me", authHandler.Me)
			auth.POST("/refresh", authHandler.Refresh)
			auth.POST("/change-password", authHandler.ChangePassword)
		}

		// OAuth routes
		oauth := v1.Group("/oauth")
		{
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

		// Repository routes
		repos := v1.Group("/repositories")
		repos.Use(authMiddleware.Middleware())
		{
			repos.GET("", repoHandler.ListRepositories)
			repos.POST("", repoHandler.CreateRepository)
			repos.GET("/:id", repoHandler.GetRepository)
			repos.PUT("/:id", repoHandler.UpdateRepository)
			repos.DELETE("/:id", repoHandler.DeleteRepository)
			repos.PATCH("/:id/status", repoHandler.SetRepositoryStatus)
			repos.POST("/:id/clone", repoHandler.TriggerClone)
			repos.GET("/:id/logs", repoHandler.GetRepositoryLogs)
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
		}

		// Incoming webhook receiver (no auth required, uses HMAC signature)
		v1.POST("/webhooks/receive", webhookHandler.ReceiveWebhook)
	}

	return router
}
