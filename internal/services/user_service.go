package services

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/gitduppy/gitduppy/internal/database"
	"github.com/gitduppy/gitduppy/internal/models"
	"github.com/gitduppy/gitduppy/pkg/crypto"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// UserService handles user CRUD operations.
type UserService struct {
	db              *gorm.DB
	passwordService *crypto.PasswordService
	authCache       AuthCacheInvalidator
}

// NewUserService creates a new user service.
func NewUserService() *UserService {
	return &UserService{
		db:              database.GetDB(),
		passwordService: crypto.NewPasswordService(),
	}
}

// SetAuthCache wires the middleware auth cache so a user status change, update or
// deletion evicts that user's cached credentials eagerly rather than letting a
// deactivated/renamed/deleted user be served for the cache TTL. Optional: a nil
// invalidator simply falls back to TTL expiry.
func (s *UserService) SetAuthCache(cache AuthCacheInvalidator) {
	s.authCache = cache
}

// UserFilter represents filters for listing users.
type UserFilter struct {
	Role     *string
	IsActive *bool
	Search   string
	Page     int
	PerPage  int
}

// ListUsers returns a paginated list of users.
func (s *UserService) ListUsers(ctx context.Context, filter *UserFilter) ([]models.User, int64, error) {
	if filter == nil {
		filter = &UserFilter{Page: 1, PerPage: 20}
	}
	if filter.Page < 1 {
		filter.Page = 1
	}
	if filter.PerPage < 1 {
		filter.PerPage = 20
	}
	if filter.PerPage > 200 {
		filter.PerPage = 200
	}

	query := s.db.WithContext(ctx).Model(&models.User{})

	// Apply filters
	if filter.Role != nil && *filter.Role != "" {
		query = query.Where("role = ?", *filter.Role)
	}
	if filter.IsActive != nil {
		query = query.Where("is_active = ?", *filter.IsActive)
	}
	if filter.Search != "" {
		query = query.Where("username LIKE ? OR email LIKE ?", "%"+filter.Search+"%", "%"+filter.Search+"%")
	}

	// Get total count
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	// Get paginated results
	offset := (filter.Page - 1) * filter.PerPage
	var users []models.User
	err := query.Offset(offset).Limit(filter.PerPage).Order("created_at DESC").Find(&users).Error
	return users, total, err
}

// GetUserByID retrieves a user by ID.
func (s *UserService) GetUserByID(ctx context.Context, id uuid.UUID) (*models.User, error) {
	var user models.User
	if err := s.db.WithContext(ctx).First(&user, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("%w: user", ErrNotFound)
		}
		return nil, err
	}
	return &user, nil
}

// GetUserByUsername retrieves a user by username.
func (s *UserService) GetUserByUsername(ctx context.Context, username string) (*models.User, error) {
	var user models.User
	if err := s.db.WithContext(ctx).Where("username = ?", username).First(&user).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("%w: user", ErrNotFound)
		}
		return nil, err
	}
	return &user, nil
}

// CreateUserRequest represents a create user request.
type CreateUserRequest struct {
	Username string `json:"username" validate:"required"`
	Email    string `json:"email" validate:"required,email"`
	Password string `json:"password" validate:"required,min=8"`
	Role     string `json:"role" validate:"required,oneof=admin user"`
}

// CreateUser creates a new user.
func (s *UserService) CreateUser(ctx context.Context, req *CreateUserRequest) (*models.User, error) {
	// Check if username exists
	var existing models.User
	if err := s.db.WithContext(ctx).Where("username = ? OR email = ?", req.Username, req.Email).First(&existing).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
	} else {
		return nil, fmt.Errorf("%w: username or email already exists", ErrConflict)
	}

	// Hash password
	hashedPassword, err := s.passwordService.Hash(req.Password)
	if err != nil {
		return nil, err
	}

	user := &models.User{
		ID:           uuid.New(),
		Username:     req.Username,
		Email:        req.Email,
		PasswordHash: &hashedPassword,
		Role:         req.Role,
		IsActive:     true,
	}

	if err := s.db.WithContext(ctx).Create(user).Error; err != nil {
		return nil, err
	}

	return user, nil
}

// UpdateUserRequest represents an update user request.
type UpdateUserRequest struct {
	Username *string `json:"username,omitempty"`
	Email    *string `json:"email,omitempty"`
	Role     *string `json:"role,omitempty"`
	IsActive *bool   `json:"is_active,omitempty"`
}

// UpdateUser updates a user.
func (s *UserService) UpdateUser(ctx context.Context, id uuid.UUID, req *UpdateUserRequest) (*models.User, error) {
	user, err := s.GetUserByID(ctx, id)
	if err != nil {
		return nil, err
	}

	// Update fields if provided
	if req.Username != nil {
		// Check if username is taken by another user
		var existing models.User
		if err := s.db.WithContext(ctx).Where("username = ? AND id != ?", *req.Username, id).First(&existing).Error; err == nil {
			return nil, fmt.Errorf("%w: username already exists", ErrConflict)
		}
		user.Username = *req.Username
	}
	if req.Email != nil {
		// Check if email is taken by another user
		var existing models.User
		if err := s.db.WithContext(ctx).Where("email = ? AND id != ?", *req.Email, id).First(&existing).Error; err == nil {
			return nil, fmt.Errorf("%w: email already exists", ErrConflict)
		}
		user.Email = *req.Email
	}
	if req.Role != nil {
		user.Role = *req.Role
	}
	if req.IsActive != nil {
		user.IsActive = *req.IsActive
	}

	user.UpdatedAt = time.Now().UTC()
	if err := s.db.WithContext(ctx).Save(user).Error; err != nil {
		return nil, err
	}

	// Evict this user's cached credentials so a role/status/identity change (e.g.
	// deactivation) takes effect immediately rather than after the cache TTL.
	if s.authCache != nil {
		s.authCache.EvictUser(id)
	}

	return user, nil
}

// DeleteUser deletes a user.
func (s *UserService) DeleteUser(ctx context.Context, id uuid.UUID) error {
	result := s.db.WithContext(ctx).Delete(&models.User{}, id)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("%w: user", ErrNotFound)
	}
	// Evict the deleted user's cached credentials so they cannot be served from the
	// auth cache for the remainder of the TTL.
	if s.authCache != nil {
		s.authCache.EvictUser(id)
	}
	return nil
}

// SetUserStatus enables or disables a user.
func (s *UserService) SetUserStatus(ctx context.Context, id uuid.UUID, isActive bool) error {
	if err := s.db.WithContext(ctx).Model(&models.User{}).Where("id = ?", id).Update("is_active", isActive).Error; err != nil {
		return err
	}
	// Evict cached credentials so a disabled user stops being authenticated
	// immediately instead of after the cache TTL.
	if s.authCache != nil {
		s.authCache.EvictUser(id)
	}
	return nil
}
