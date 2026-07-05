package services

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gitduppy/gitduppy/internal/database"
	"github.com/gitduppy/gitduppy/internal/models"
	"github.com/gitduppy/gitduppy/pkg/crypto"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// maxRepositoriesPerPage caps list page size so a single request cannot pull the
// entire repositories table (and eager-load every row's tags/user).
const maxRepositoriesPerPage = 200

// RepositoryService handles repository CRUD operations.
type RepositoryService struct {
	db                *gorm.DB
	encryptionService *crypto.EncryptionService
	basePath          string
}

// NewRepositoryService creates a new repository service. basePath is the storage
// root under which every repository's sharded working tree lives; it is baked
// into each repository's StoragePath at creation so that all consumers (browse,
// delete, restore, paperbin, cleanup) resolve the same on-disk location without
// having to re-join the base path themselves.
func NewRepositoryService(encryptionService *crypto.EncryptionService, basePath string) *RepositoryService {
	return &RepositoryService{
		db:                database.GetDB(),
		encryptionService: encryptionService,
		basePath:          basePath,
	}
}

// RepositoryFilter represents filters for listing repositories.
type RepositoryFilter struct {
	Status   *string
	Tag      *string
	Search   string
	IsActive *bool
	Page     int
	PerPage  int
	Sort     string
}

// ListRepositories returns a paginated list of repositories.
func (s *RepositoryService) ListRepositories(_ context.Context, filter *RepositoryFilter) ([]models.Repository, int64, error) {
	if filter == nil {
		filter = &RepositoryFilter{Page: 1, PerPage: 20}
	}
	if filter.Page < 1 {
		filter.Page = 1
	}
	if filter.PerPage < 1 {
		filter.PerPage = 20
	}
	// Cap page size so a client cannot pull the entire table in one request.
	if filter.PerPage > maxRepositoriesPerPage {
		filter.PerPage = maxRepositoriesPerPage
	}

	query := s.db.Model(&models.Repository{}).Preload("Tags").Preload("CreatedByUser")

	// Apply filters
	if filter.Status != nil && *filter.Status != "" {
		query = query.Where("status = ?", *filter.Status)
	}
	if filter.IsActive != nil {
		query = query.Where("is_active = ?", *filter.IsActive)
	}
	if filter.Search != "" {
		query = query.Where("name LIKE ? OR url LIKE ?", "%"+filter.Search+"%", "%"+filter.Search+"%")
	}
	if filter.Tag != nil && *filter.Tag != "" {
		query = query.Joins("JOIN repository_tags ON repository_tags.repository_id = repositories.id").
			Joins("JOIN tags ON tags.id = repository_tags.tag_id").
			Where("tags.name = ?", *filter.Tag)
	}

	// Get total count
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	// Apply sorting
	switch filter.Sort {
	case "-last_clone_at":
		query = query.Order("last_clone_at DESC NULLS LAST")
	case "last_clone_at":
		query = query.Order("last_clone_at ASC NULLS LAST")
	case "-created_at":
		query = query.Order("created_at DESC")
	default:
		query = query.Order("name ASC")
	}

	// Get paginated results
	offset := (filter.Page - 1) * filter.PerPage
	var repos []models.Repository
	err := query.Offset(offset).Limit(filter.PerPage).Find(&repos).Error
	return repos, total, err
}

// GetRepositoryByID retrieves a repository by ID.
func (s *RepositoryService) GetRepositoryByID(_ context.Context, id uuid.UUID) (*models.Repository, error) {
	var repo models.Repository
	if err := s.db.Preload("Tags").Preload("CreatedByUser").First(&repo, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("repository not found")
		}
		return nil, err
	}
	return &repo, nil
}

// CreateRepositoryRequest represents a create repository request.
type CreateRepositoryRequest struct {
	Name        string                     `json:"name" validate:"required"`
	URL         string                     `json:"url" validate:"required"`
	Branch      string                     `json:"branch" validate:"required"`
	AuthType    string                     `json:"auth_type" validate:"required,oneof=none https ssh token"`
	Credentials *crypto.CredentialsPayload `json:"credentials,omitempty"`
	// Note: storage path is derived server-side from the configured storage base
	// and the repository ID; it is intentionally not accepted from the request.
	IsBare               bool        `json:"is_bare"`
	LFSEnabled           bool        `json:"lfs_enabled"`
	MirrorIssues         bool        `json:"mirror_issues"`
	MirrorPullRequests   bool        `json:"mirror_pull_requests"`
	MirrorReleases       bool        `json:"mirror_releases"`
	MirrorWiki           bool        `json:"mirror_wiki"`
	CloneIntervalMinutes int         `json:"clone_interval_minutes" validate:"min=5"`
	RetentionDays        int         `json:"retention_days"`
	Description          *string     `json:"description,omitempty"`
	TagIDs               []uuid.UUID `json:"tag_ids,omitempty"`
}

// CreateRepository creates a new repository.
func (s *RepositoryService) CreateRepository(_ context.Context, req *CreateRepositoryRequest, createdBy uuid.UUID) (*models.Repository, error) {
	// Encrypt credentials if provided
	var encryptedCredentials string
	if req.Credentials != nil {
		var err error
		encryptedCredentials, err = s.encryptionService.Encrypt(*req.Credentials)
		if err != nil {
			return nil, err
		}
	}

	repoID := uuid.New()
	idStr := repoID.String()

	// Shard storage path: basePath/shards/ab/cd/uuid to prevent filesystem limits.
	// The base storage root is joined in here so the persisted StoragePath is the
	// full on-disk location; every consumer uses it directly without re-joining.
	storagePath := filepath.Join(s.basePath, "shards", idStr[0:2], idStr[2:4], idStr)

	retentionDays := req.RetentionDays
	if retentionDays <= 0 {
		retentionDays = 30
	}

	repo := &models.Repository{
		ID:                   repoID,
		Name:                 req.Name,
		URL:                  req.URL,
		Branch:               req.Branch,
		AuthType:             req.AuthType,
		EncryptedCredentials: encryptedCredentials,
		StoragePath:          storagePath,
		Status:               "pending",
		IsBare:               req.IsBare,
		LFSEnabled:           req.LFSEnabled,
		MirrorIssues:         req.MirrorIssues,
		MirrorPullRequests:   req.MirrorPullRequests,
		MirrorReleases:       req.MirrorReleases,
		MirrorWiki:           req.MirrorWiki,
		IsActive:             true,
		CloneIntervalMinutes: req.CloneIntervalMinutes,
		RetentionDays:        retentionDays,
		Description:          req.Description,
		CreatedBy:            &createdBy,
	}

	if err := s.db.Create(repo).Error; err != nil {
		return nil, err
	}

	// Associate tags if provided
	if len(req.TagIDs) > 0 {
		var tags []models.Tag
		if err := s.db.Where("id IN ?", req.TagIDs).Find(&tags).Error; err != nil {
			return nil, err
		}
		if err := s.db.Model(repo).Association("Tags").Append(tags); err != nil {
			return nil, err
		}
	}

	return repo, nil
}

// UpdateRepositoryRequest represents an update repository request.
type UpdateRepositoryRequest struct {
	Name                 *string                    `json:"name,omitempty"`
	URL                  *string                    `json:"url,omitempty"`
	Branch               *string                    `json:"branch,omitempty"`
	AuthType             *string                    `json:"auth_type,omitempty"`
	Credentials          *crypto.CredentialsPayload `json:"credentials,omitempty"`
	StoragePath          *string                    `json:"storage_path,omitempty"`
	IsBare               *bool                      `json:"is_bare,omitempty"`
	LFSEnabled           *bool                      `json:"lfs_enabled,omitempty"`
	MirrorIssues         *bool                      `json:"mirror_issues,omitempty"`
	MirrorPullRequests   *bool                      `json:"mirror_pull_requests,omitempty"`
	MirrorReleases       *bool                      `json:"mirror_releases,omitempty"`
	MirrorWiki           *bool                      `json:"mirror_wiki,omitempty"`
	IsActive             *bool                      `json:"is_active,omitempty"`
	CloneIntervalMinutes *int                       `json:"clone_interval_minutes,omitempty"`
	RetentionDays        *int                       `json:"retention_days,omitempty"`
	Description          *string                    `json:"description,omitempty"`
	TagIDs               []uuid.UUID                `json:"tag_ids,omitempty"`
}

// UpdateRepository updates a repository.
func (s *RepositoryService) UpdateRepository(ctx context.Context, id uuid.UUID, req *UpdateRepositoryRequest) (*models.Repository, error) {
	repo, err := s.GetRepositoryByID(ctx, id)
	if err != nil {
		return nil, err
	}

	if err := s.applyUpdateFields(repo, req); err != nil {
		return nil, err
	}

	repo.UpdatedAt = time.Now()

	// Use a transaction so Save and tag replacement are atomic.
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Save(repo).Error; err != nil {
			return err
		}

		// Update tags if provided
		if req.TagIDs != nil {
			var tags []models.Tag
			if err := tx.Where("id IN ?", req.TagIDs).Find(&tags).Error; err != nil {
				return err
			}
			if err := tx.Model(repo).Association("Tags").Replace(tags); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	return repo, nil
}

func (s *RepositoryService) applyUpdateFields(repo *models.Repository, req *UpdateRepositoryRequest) error {
	if req.Name != nil {
		repo.Name = *req.Name
	}
	if req.URL != nil {
		repo.URL = *req.URL
	}
	if req.Branch != nil {
		repo.Branch = *req.Branch
	}
	if req.AuthType != nil {
		repo.AuthType = *req.AuthType
	}
	if req.Credentials != nil {
		encrypted, err := s.encryptionService.Encrypt(*req.Credentials)
		if err != nil {
			return err
		}
		repo.EncryptedCredentials = encrypted
	}
	if req.StoragePath != nil {
		repo.StoragePath = *req.StoragePath
	}
	if req.IsBare != nil {
		repo.IsBare = *req.IsBare
	}
	if req.LFSEnabled != nil {
		repo.LFSEnabled = *req.LFSEnabled
	}
	if req.IsActive != nil {
		repo.IsActive = *req.IsActive
	}
	if req.CloneIntervalMinutes != nil {
		repo.CloneIntervalMinutes = *req.CloneIntervalMinutes
	}
	if req.RetentionDays != nil {
		repo.RetentionDays = *req.RetentionDays
	}
	if req.Description != nil {
		repo.Description = req.Description
	}
	if req.MirrorIssues != nil {
		repo.MirrorIssues = *req.MirrorIssues
	}
	if req.MirrorPullRequests != nil {
		repo.MirrorPullRequests = *req.MirrorPullRequests
	}
	if req.MirrorReleases != nil {
		repo.MirrorReleases = *req.MirrorReleases
	}
	if req.MirrorWiki != nil {
		repo.MirrorWiki = *req.MirrorWiki
	}
	return nil
}

// DeleteRepository soft-deletes a repository and moves its compressed folder to the paperbin.
func (s *RepositoryService) DeleteRepository(_ context.Context, id uuid.UUID) error {
	var repo models.Repository
	if err := s.db.First(&repo, id).Error; err != nil {
		return err
	}

	// Compress local folder and move to paperbin
	paperbinPath := filepath.Join(filepath.Dir(repo.StoragePath), "paperbin", repo.ID.String())
	tarGzPath := paperbinPath + ".tar.gz"

	if _, err := os.Stat(repo.StoragePath); err == nil {
		if err := os.MkdirAll(filepath.Dir(paperbinPath), 0o750); err != nil {
			return fmt.Errorf("failed to create paperbin parent directory: %w", err)
		}
		// In case a paperbin archive already exists, remove it first
		_ = os.Remove(tarGzPath)

		if err := tarGzCompress(repo.StoragePath, tarGzPath); err != nil {
			return fmt.Errorf("failed to compress repository to paperbin: %w", err)
		}

		// Remove original folder after successful compression
		_ = os.RemoveAll(repo.StoragePath)
	}

	// Perform soft delete
	result := s.db.Delete(&repo)
	if result.Error != nil {
		// If DB delete fails, try to decompress folder back
		if _, err := os.Stat(tarGzPath); err == nil {
			_ = tarGzDecompress(tarGzPath, repo.StoragePath)
			_ = os.Remove(tarGzPath)
		}
		return result.Error
	}
	return nil
}

// RestoreRepository restores a soft-deleted repository.
func (s *RepositoryService) RestoreRepository(_ context.Context, id uuid.UUID) error {
	var repo models.Repository
	if err := s.db.Unscoped().Where("id = ? AND deleted_at IS NOT NULL", id).First(&repo).Error; err != nil {
		return err
	}

	// Decompress local folder back from paperbin
	paperbinPath := filepath.Join(filepath.Dir(repo.StoragePath), "paperbin", repo.ID.String())
	tarGzPath := paperbinPath + ".tar.gz"

	if _, err := os.Stat(tarGzPath); err == nil {
		if err := os.MkdirAll(filepath.Dir(repo.StoragePath), 0o750); err != nil {
			return fmt.Errorf("failed to create repository parent directory: %w", err)
		}
		// In case a destination folder already exists, remove it
		_ = os.RemoveAll(repo.StoragePath)

		if err := tarGzDecompress(tarGzPath, repo.StoragePath); err != nil {
			return fmt.Errorf("failed to restore repository from paperbin archive: %w", err)
		}

		// Delete compressed file
		_ = os.Remove(tarGzPath)
	} else if _, err := os.Stat(paperbinPath); err == nil {
		// Fallback for uncompressed folders
		_ = os.RemoveAll(repo.StoragePath)
		if err := os.Rename(paperbinPath, repo.StoragePath); err != nil {
			return fmt.Errorf("failed to restore repository from paperbin directory: %w", err)
		}
	}

	// Restore DB record
	if err := s.db.Unscoped().Model(&repo).Update("deleted_at", nil).Error; err != nil {
		// If DB update fails, try to compress folder back to paperbin
		_ = tarGzCompress(repo.StoragePath, tarGzPath)
		_ = os.RemoveAll(repo.StoragePath)
		return err
	}
	return nil
}

// PermanentDeleteRepository permanently deletes a repository and its files.
func (s *RepositoryService) PermanentDeleteRepository(_ context.Context, id uuid.UUID) error {
	var repo models.Repository
	if err := s.db.Unscoped().First(&repo, id).Error; err != nil {
		return err
	}

	// Delete related DeletedBranches
	if err := s.db.Where("repository_id = ?", id).Delete(&models.DeletedBranch{}).Error; err != nil {
		return err
	}

	// Delete related CloneJobs and logs
	if err := s.db.Where("repository_id = ?", id).Delete(&models.CloneJob{}).Error; err != nil {
		return err
	}

	// Perform hard delete in DB
	if err := s.db.Unscoped().Delete(&repo).Error; err != nil {
		return err
	}

	// Delete paperbin compressed archive and uncompressed folders on disk
	paperbinPath := filepath.Join(filepath.Dir(repo.StoragePath), "paperbin", repo.ID.String())
	_ = os.Remove(paperbinPath + ".tar.gz")
	_ = os.RemoveAll(paperbinPath)
	// Also delete normal storage path in case it wasn't moved/compressed
	_ = os.RemoveAll(repo.StoragePath)

	return nil
}

// SetRepositoryStatus enables or disables a repository.
func (s *RepositoryService) SetRepositoryStatus(_ context.Context, id uuid.UUID, isActive bool) error {
	result := s.db.Model(&models.Repository{}).Where("id = ?", id).Update("is_active", isActive)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return errors.New("repository not found")
	}
	return nil
}

// GetDecryptedCredentials returns the decrypted credentials for a repository.
func (s *RepositoryService) GetDecryptedCredentials(_ context.Context, repoID uuid.UUID) (*crypto.CredentialsPayload, error) {
	var repo models.Repository
	if err := s.db.First(&repo, repoID).Error; err != nil {
		return nil, err
	}

	if repo.EncryptedCredentials == "" {
		return &crypto.CredentialsPayload{}, nil
	}

	payload, err := s.encryptionService.Decrypt(repo.EncryptedCredentials)
	if err != nil {
		return nil, err
	}
	return &payload, nil
}

// tarGzCompress archives and compresses a directory into a .tar.gz file.
func tarGzCompress(srcDir, destFile string) error {
	d, err := os.Create(destFile)
	if err != nil {
		return err
	}

	gw := gzip.NewWriter(d)
	tw := tar.NewWriter(gw)

	walkErr := filepath.Walk(srcDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Preserve symlinks as links rather than dereferencing/copying their
		// target contents.
		linkTarget := ""
		if info.Mode()&os.ModeSymlink != 0 {
			linkTarget, err = os.Readlink(path)
			if err != nil {
				return err
			}
		}

		header, err := tar.FileInfoHeader(info, linkTarget)
		if err != nil {
			return err
		}

		relPath, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}
		header.Name = filepath.ToSlash(relPath)

		if err := tw.WriteHeader(header); err != nil {
			return err
		}

		// Only regular files have their bytes copied; directories and symlinks
		// are fully described by the header alone.
		if !info.Mode().IsRegular() {
			return nil
		}

		// #nosec G122 - path is generated during walk of controlled backup directory, not user input
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer file.Close()

		_, err = io.Copy(tw, file)
		return err
	})

	// Close writers explicitly in order and propagate any close error so a
	// failed flush is not reported as success.
	if walkErr != nil {
		_ = tw.Close()
		_ = gw.Close()
		_ = d.Close()
		return walkErr
	}
	if err := tw.Close(); err != nil {
		_ = gw.Close()
		_ = d.Close()
		return err
	}
	if err := gw.Close(); err != nil {
		_ = d.Close()
		return err
	}
	return d.Close()
}

// tarGzDecompress extracts a .tar.gz file into a destination directory.
func tarGzDecompress(srcFile, destDir string) error {
	r, err := os.Open(srcFile)
	if err != nil {
		return err
	}
	defer r.Close()

	gr, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	defer gr.Close()

	tr := tar.NewReader(gr)

	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		// Guard against path traversal (Zip Slip): sanitize the header name
		// before doing any joins or filesystem operations.
		cleanName := filepath.Clean(header.Name)
		if !filepath.IsLocal(cleanName) {
			return fmt.Errorf("invalid path in archive (path traversal): %s", header.Name)
		}

		target := filepath.Join(destDir, cleanName)

		// Extra safety check: ensure the resolved target stays within destDir
		if !strings.HasPrefix(target, filepath.Clean(destDir)+string(os.PathSeparator)) {
			return fmt.Errorf("invalid path in archive (path traversal): %s", header.Name)
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			outFile, err := os.OpenFile(target, os.O_CREATE|os.O_RDWR|os.O_TRUNC, header.FileInfo().Mode())
			if err != nil {
				return err
			}
			// #nosec G110 - Decompression bomb protection is handled at a higher level, and backups are system-generated
			if _, err := io.Copy(outFile, tr); err != nil {
				_ = outFile.Close()
				return err
			}
			if err := outFile.Close(); err != nil {
				return err
			}
		}
	}
	return nil
}
