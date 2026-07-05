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

	createPerformanceIndexes()

	log.Println("Database migrations completed successfully")
	return nil
}

// createPerformanceIndexes adds indexes for the hot query paths (dashboard
// aggregates, the scheduler scan, per-repo job listing, and the search LIKE).
// All are IF NOT EXISTS so the step is idempotent across restarts. Failures are
// logged but not fatal — a missing index degrades performance, not correctness.
func createPerformanceIndexes() {
	indexes := []string{
		`CREATE INDEX IF NOT EXISTS idx_clone_jobs_status ON clone_jobs(status)`,
		`CREATE INDEX IF NOT EXISTS idx_clone_jobs_repo_created ON clone_jobs(repository_id, created_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_clone_jobs_created_at ON clone_jobs(created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_repositories_active_status ON repositories(is_active, status)`,
		`CREATE INDEX IF NOT EXISTS idx_repositories_last_clone_at ON repositories(last_clone_at)`,
		`CREATE INDEX IF NOT EXISTS idx_audit_logs_created_at ON audit_logs(created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_deleted_branches_repo ON deleted_branches(repository_id)`,
	}
	for _, stmt := range indexes {
		if err := DB.Exec(stmt).Error; err != nil {
			log.Printf("index creation skipped (%q): %v", stmt, err)
		}
	}

	// Trigram indexes accelerate the repository search's LIKE '%term%' (which a
	// btree cannot use). Requires the pg_trgm extension; creating it needs
	// elevated privileges, so treat any failure here as best-effort.
	if err := DB.Exec(`CREATE EXTENSION IF NOT EXISTS pg_trgm`).Error; err != nil {
		log.Printf("pg_trgm extension unavailable, skipping trigram indexes: %v", err)
		return
	}
	trgm := []string{
		`CREATE INDEX IF NOT EXISTS idx_repositories_name_trgm ON repositories USING gin (name gin_trgm_ops)`,
		`CREATE INDEX IF NOT EXISTS idx_repositories_url_trgm ON repositories USING gin (url gin_trgm_ops)`,
	}
	for _, stmt := range trgm {
		if err := DB.Exec(stmt).Error; err != nil {
			log.Printf("trigram index creation skipped (%q): %v", stmt, err)
		}
	}
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
// moveIfPresent moves src to dst when src exists and dst does not yet, creating
// dst's parent directory first. A missing src is a no-op (returns false, nil); an
// already-populated dst is left untouched (false, nil). Returns (moved, err).
func moveIfPresent(src, dst string) (bool, error) {
	if _, err := os.Stat(src); err != nil {
		return false, nil //nolint:nilerr // missing source: nothing to move
	}
	if _, err := os.Stat(dst); err == nil {
		return false, nil // destination already present; leave as-is
	} else if !os.IsNotExist(err) {
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0750); err != nil {
		return false, err
	}
	if err := os.Rename(src, dst); err != nil {
		return false, err
	}
	return true, nil
}

func MigrateStoragePaths(basePath string) error {
	if DB == nil {
		return fmt.Errorf("database not connected")
	}

	var repos []models.Repository
	// Unscoped so soft-deleted rows (whose paperbin archives must be relocated
	// too) are normalized. Only id + storage_path are needed, so project just
	// those columns rather than hydrating the whole table on every startup.
	if err := DB.Unscoped().Select("id", "storage_path").Find(&repos).Error; err != nil {
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
		if old != "" {
			oldDir, newDir := filepath.Dir(old), filepath.Dir(canonical)

			// Relocate the working tree. If this required move fails, keep the old
			// pointer so an existing clone stays reachable rather than dangling.
			if _, err := moveIfPresent(old, canonical); err != nil {
				log.Printf("storage-path migration: cannot move %q -> %q: %v (leaving pointer unchanged)", old, canonical, err)
				continue
			}

			// Relocate the companion wiki mirror (StoragePath + ".wiki"). Non-fatal:
			// a missing wiki is simply re-cloned on the next sync.
			if _, err := moveIfPresent(old+".wiki", canonical+".wiki"); err != nil {
				log.Printf("storage-path migration: cannot move wiki for %s: %v (wiki will be re-cloned)", id, err)
			}

			// Relocate the paperbin archive of a soft-deleted (or previously
			// deleted) repo. Delete/Restore resolve it from filepath.Dir(StoragePath)
			// + "/paperbin/<id>", so rewriting the pointer without moving the archive
			// would orphan it and make restore silently lose the files. If this move
			// fails, keep the old pointer so restore still finds the archive.
			pbFail := false
			for _, name := range []string{id + ".tar.gz", id} {
				src := filepath.Join(oldDir, "paperbin", name)
				dst := filepath.Join(newDir, "paperbin", name)
				if _, err := moveIfPresent(src, dst); err != nil {
					log.Printf("storage-path migration: cannot move paperbin %q -> %q: %v (leaving pointer unchanged)", src, dst, err)
					pbFail = true
					break
				}
			}
			if pbFail {
				continue
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
