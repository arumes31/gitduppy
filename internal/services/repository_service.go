package services

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/gitduppy/gitduppy/internal/database"
	"github.com/gitduppy/gitduppy/internal/models"
	"github.com/gitduppy/gitduppy/pkg/crypto"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// RepositoryService handles repository CRUD operations.
type RepositoryService struct {
	db                *gorm.DB
	encryptionService *crypto.EncryptionService
}

// NewRepositoryService creates a new repository service.
func NewRepositoryService(encryptionService *crypto.EncryptionService) *RepositoryService {
	return &RepositoryService{
		db:                database.GetDB(),
		encryptionService: encryptionService,
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
	Name                 string                     `json:"name" validate:"required"`
	URL                  string                     `json:"url" validate:"required"`
	Branch               string                     `json:"branch" validate:"required"`
	AuthType             string                     `json:"auth_type" validate:"required,oneof=none https ssh token"`
	Credentials          *crypto.CredentialsPayload `json:"credentials,omitempty"`
	StoragePath          string                     `json:"storage_path" validate:"required"`
	IsBare               bool                       `json:"is_bare"`
	LFSEnabled           bool                       `json:"lfs_enabled"`
	MirrorIssues         bool                       `json:"mirror_issues"`
	MirrorPullRequests   bool                       `json:"mirror_pull_requests"`
	MirrorReleases       bool                       `json:"mirror_releases"`
	MirrorWiki           bool                       `json:"mirror_wiki"`
	CloneIntervalMinutes int                        `json:"clone_interval_minutes" validate:"min=5"`
	Description          *string                    `json:"description,omitempty"`
	TagIDs               []uuid.UUID                `json:"tag_ids,omitempty"`
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

	repo := &models.Repository{
		ID:                   uuid.New(),
		Name:                 req.Name,
		URL:                  req.URL,
		Branch:               req.Branch,
		AuthType:             req.AuthType,
		EncryptedCredentials: encryptedCredentials,
		StoragePath:          req.StoragePath,
		Status:               "pending",
		IsBare:               req.IsBare,
		LFSEnabled:           req.LFSEnabled,
		MirrorIssues:         req.MirrorIssues,
		MirrorPullRequests:   req.MirrorPullRequests,
		MirrorReleases:       req.MirrorReleases,
		MirrorWiki:           req.MirrorWiki,
		IsActive:             true,
		CloneIntervalMinutes: req.CloneIntervalMinutes,
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

// DeleteRepository soft-deletes a repository and moves its folder to the paperbin.
func (s *RepositoryService) DeleteRepository(_ context.Context, id uuid.UUID) error {
	var repo models.Repository
	if err := s.db.First(&repo, id).Error; err != nil {
		return err
	}

	// Move local folder to paperbin
	paperbinPath := filepath.Join(filepath.Dir(repo.StoragePath), "paperbin", repo.ID.String())
	if _, err := os.Stat(repo.StoragePath); err == nil {
		if err := os.MkdirAll(filepath.Dir(paperbinPath), 0750); err != nil {
			return fmt.Errorf("failed to create paperbin parent directory: %w", err)
		}
		// In case a paperbin folder already exists, remove it first
		_ = os.RemoveAll(paperbinPath)
		if err := os.Rename(repo.StoragePath, paperbinPath); err != nil {
			return fmt.Errorf("failed to move repository to paperbin: %w", err)
		}
	}

	// Perform soft delete
	result := s.db.Delete(&repo)
	if result.Error != nil {
		// If DB delete fails, try to move folder back
		_ = os.Rename(paperbinPath, repo.StoragePath)
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

	// Move local folder back from paperbin
	paperbinPath := filepath.Join(filepath.Dir(repo.StoragePath), "paperbin", repo.ID.String())
	if _, err := os.Stat(paperbinPath); err == nil {
		if err := os.MkdirAll(filepath.Dir(repo.StoragePath), 0750); err != nil {
			return fmt.Errorf("failed to create repository parent directory: %w", err)
		}
		// In case a destination folder already exists, remove it
		_ = os.RemoveAll(repo.StoragePath)
		if err := os.Rename(paperbinPath, repo.StoragePath); err != nil {
			return fmt.Errorf("failed to restore repository from paperbin: %w", err)
		}
	}

	// Restore DB record
	if err := s.db.Unscoped().Model(&repo).Update("deleted_at", nil).Error; err != nil {
		// If DB update fails, try to move folder back to paperbin
		_ = os.Rename(repo.StoragePath, paperbinPath)
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

	// Delete paperbin folder on disk
	paperbinPath := filepath.Join(filepath.Dir(repo.StoragePath), "paperbin", repo.ID.String())
	_ = os.RemoveAll(paperbinPath)
	// Also delete normal storage path in case it wasn't moved
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
