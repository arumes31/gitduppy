package services

import (
	"context"
	"errors"
	"time"

	"github.com/gitduppy/gitduppy/internal/database"
	"github.com/gitduppy/gitduppy/internal/models"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// TagService handles tag management
type TagService struct {
	db *gorm.DB
}

// NewTagService creates a new tag service
func NewTagService() *TagService {
	return &TagService{
		db: database.GetDB(),
	}
}

// ListTags returns all tags
func (s *TagService) ListTags(ctx context.Context) ([]models.Tag, error) {
	var tags []models.Tag
	err := s.db.Order("name ASC").Find(&tags).Error
	return tags, err
}

// GetTagByID retrieves a tag by ID
func (s *TagService) GetTagByID(ctx context.Context, id uuid.UUID) (*models.Tag, error) {
	var tag models.Tag
	if err := s.db.First(&tag, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("tag not found")
		}
		return nil, err
	}
	return &tag, nil
}

// GetTagByName retrieves a tag by name
func (s *TagService) GetTagByName(ctx context.Context, name string) (*models.Tag, error) {
	var tag models.Tag
	if err := s.db.Where("name = ?", name).First(&tag).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("tag not found")
		}
		return nil, err
	}
	return &tag, nil
}

// CreateTagRequest represents a create tag request
type CreateTagRequest struct {
	Name  string `json:"name" validate:"required"`
	Color string `json:"color" validate:"required"`
}

// CreateTag creates a new tag
func (s *TagService) CreateTag(ctx context.Context, req *CreateTagRequest) (*models.Tag, error) {
	// Check if tag name exists
	var existing models.Tag
	if err := s.db.Where("name = ?", req.Name).First(&existing).Error; err == nil {
		return nil, errors.New("tag name already exists")
	}

	tag := &models.Tag{
		ID:        uuid.New(),
		Name:      req.Name,
		Color:     req.Color,
		CreatedAt: time.Now(),
	}

	if err := s.db.Create(tag).Error; err != nil {
		return nil, err
	}

	return tag, nil
}

// UpdateTagRequest represents an update tag request
type UpdateTagRequest struct {
	Name  *string `json:"name,omitempty"`
	Color *string `json:"color,omitempty"`
}

// UpdateTag updates a tag
func (s *TagService) UpdateTag(ctx context.Context, id uuid.UUID, req *UpdateTagRequest) (*models.Tag, error) {
	tag, err := s.GetTagByID(ctx, id)
	if err != nil {
		return nil, err
	}

	if req.Name != nil {
		// Check if name is taken by another tag
		var existing models.Tag
		if err := s.db.Where("name = ? AND id != ?", *req.Name, id).First(&existing).Error; err == nil {
			return nil, errors.New("tag name already exists")
		}
		tag.Name = *req.Name
	}
	if req.Color != nil {
		tag.Color = *req.Color
	}

	if err := s.db.Save(tag).Error; err != nil {
		return nil, err
	}

	return tag, nil
}

// DeleteTag deletes a tag
func (s *TagService) DeleteTag(ctx context.Context, id uuid.UUID) error {
	result := s.db.Delete(&models.Tag{}, id)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return errors.New("tag not found")
	}
	return nil
}

// AddTagToRepository adds a tag to a repository
func (s *TagService) AddTagToRepository(ctx context.Context, repoID, tagID uuid.UUID) error {
	// Check if association already exists
	var existing models.RepositoryTag
	if err := s.db.Where("repository_id = ? AND tag_id = ?", repoID, tagID).First(&existing).Error; err == nil {
		return errors.New("tag already assigned to repository")
	}

	repoTag := &models.RepositoryTag{
		ID:           uuid.New(),
		RepositoryID: repoID,
		TagID:        tagID,
		CreatedAt:    time.Now(),
	}

	return s.db.Create(repoTag).Error
}

// RemoveTagFromRepository removes a tag from a repository
func (s *TagService) RemoveTagFromRepository(ctx context.Context, repoID, tagID uuid.UUID) error {
	result := s.db.Where("repository_id = ? AND tag_id = ?", repoID, tagID).Delete(&models.RepositoryTag{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return errors.New("tag not assigned to repository")
	}
	return nil
}

// GetRepositoryTags retrieves all tags for a repository
func (s *TagService) GetRepositoryTags(ctx context.Context, repoID uuid.UUID) ([]models.Tag, error) {
	var tags []models.Tag
	err := s.db.Model(&models.Tag{}).
		Joins("JOIN repository_tags ON repository_tags.tag_id = tags.id").
		Where("repository_tags.repository_id = ?", repoID).
		Find(&tags).Error
	return tags, err
}

// SetRepositoryTags replaces all tags on a repository
func (s *TagService) SetRepositoryTags(ctx context.Context, repoID uuid.UUID, tagIDs []uuid.UUID) error {
	// Delete existing associations
	if err := s.db.Where("repository_id = ?", repoID).Delete(&models.RepositoryTag{}).Error; err != nil {
		return err
	}

	// Add new associations
	for _, tagID := range tagIDs {
		repoTag := &models.RepositoryTag{
			ID:           uuid.New(),
			RepositoryID: repoID,
			TagID:        tagID,
			CreatedAt:    time.Now(),
		}
		if err := s.db.Create(repoTag).Error; err != nil {
			return err
		}
	}

	return nil
}
