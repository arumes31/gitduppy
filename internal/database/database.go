package database

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

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
		&models.DeletedBranch{},
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

// MigrateStoragePaths brings every repository's StoragePath into the canonical
// form baked in by the current RepositoryService: basePath/shards/ab/cd/uuid.
//
// Older builds persisted a base-relative or differently-rooted path (e.g.
// "repos/shards/..") and consumers re-joined the base at read time. Consumers now
// use StoragePath verbatim, so any row whose stored path is not already canonical
// would resolve to the wrong location after upgrade. This one-time, idempotent
// pass rewrites the DB pointer and, when the on-disk tree still lives at the old
// location, relocates it so an existing clone is not lost or re-fetched into a
// stray directory.
func MigrateStoragePaths(basePath string) error {
	if DB == nil {
		return fmt.Errorf("database not connected")
	}

	var repos []models.Repository
	// Unscoped so soft-deleted rows (whose archives live under the same tree) are
	// normalized too.
	if err := DB.Unscoped().Find(&repos).Error; err != nil {
		return fmt.Errorf("failed to load repositories for storage-path migration: %w", err)
	}

	migrated := 0
	for i := range repos {
		repo := &repos[i]
		id := repo.ID.String()
		if len(id) < 4 {
			continue
		}
		canonical := filepath.Join(basePath, "shards", id[0:2], id[2:4], id)
		if repo.StoragePath == canonical {
			continue // already canonical (new deployments, or basePath unchanged)
		}

		old := repo.StoragePath
		// Relocate the working tree only when it still sits at the old path and the
		// canonical location is free, so no existing clone is stranded. All other
		// cases (nothing cloned yet, or already present at canonical) just need the
		// pointer rewritten.
		if old != "" {
			_, oldErr := os.Stat(old)
			_, newErr := os.Stat(canonical)
			if oldErr == nil && os.IsNotExist(newErr) {
				if mkErr := os.MkdirAll(filepath.Dir(canonical), 0750); mkErr != nil {
					log.Printf("storage-path migration: cannot create parent for %s: %v (leaving %q in place)", canonical, mkErr, old)
					continue
				}
				if mvErr := os.Rename(old, canonical); mvErr != nil {
					// Cross-device or permission failure: keep the old pointer so the
					// existing clone stays reachable rather than pointing at nothing.
					log.Printf("storage-path migration: cannot move %q -> %q: %v (leaving pointer unchanged)", old, canonical, mvErr)
					continue
				}
			}
		}

		if err := DB.Unscoped().Model(&models.Repository{}).Where("id = ?", repo.ID).Update("storage_path", canonical).Error; err != nil {
			return fmt.Errorf("failed to update storage_path for %s: %w", id, err)
		}
		migrated++
	}

	if migrated > 0 {
		log.Printf("storage-path migration: normalized %d repository path(s)", migrated)
	}
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
