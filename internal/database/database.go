package database

import (
	"fmt"
	"log"

	"github.com/gitduppy/gitduppy/internal/config"
	"github.com/gitduppy/gitduppy/internal/models"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

//nolint:gochecknoglobals
var DB *gorm.DB

// Connect establishes a connection to the PostgreSQL database.
func Connect(cfg *config.DatabaseConfig) error {
	dsn := cfg.DSN()

	// Configure GORM logger (can be made configurable later).
	ormLogger := logger.Default

	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
		Logger: ormLogger,
	})
	if err != nil {
		return fmt.Errorf("failed to connect to database: %w", err)
	}

	// Get underlying SQL DB to configure connection pool
	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("failed to get underlying SQL DB: %w", err)
	}

	sqlDB.SetMaxOpenConns(cfg.MaxOpenConns)
	sqlDB.SetMaxIdleConns(cfg.MaxIdleConns)
	sqlDB.SetConnMaxLifetime(cfg.ConnMaxLifetime)

	DB = db
	log.Println("Database connection established")
	return nil
}

// AutoMigrate runs automatic migrations for all models.
func AutoMigrate() error {
	if DB == nil {
		return fmt.Errorf("database not connected")
	}

	err := DB.AutoMigrate(
		&models.User{},
		&models.Repository{},
		&models.CloneJob{},
		&models.CloneLog{},
		&models.APIKey{},
		&models.WebhookConfig{},
		&models.WebhookDelivery{},
		&models.AuditLog{},
		&models.Tag{},
		&models.RepositoryTag{},
		&models.Session{},
		&models.HealthCheck{},
		&models.SystemSetting{},
	)
	if err != nil {
		return fmt.Errorf("migration failed: %w", err)
	}

	log.Println("Database migrations completed successfully")
	return nil
}

// GetDB returns the database instance.
func GetDB() *gorm.DB {
	return DB
}

// Close closes the database connection.
func Close() error {
	if DB == nil {
		return nil
	}
	sqlDB, err := DB.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}
