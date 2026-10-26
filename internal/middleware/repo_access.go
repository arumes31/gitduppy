package middleware

import (
	"errors"

	"github.com/gin-gonic/gin"
	"github.com/gitduppy/gitduppy/internal/models"
	"github.com/gitduppy/gitduppy/pkg/response"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// RequireRepoOwnerOrAdmin returns a middleware that only lets the repository's
// creator or an admin through. It guards the mutating repository routes
// (update, delete, restore, purge, status, clone trigger, paperbin and tag
// changes) so one account cannot repoint, wipe or re-clone another user's
// mirror, or redirect its stored credentials to a different host.
//
// The repository is resolved from the named path parameter, including
// soft-deleted rows (restore and purge operate on those). A malformed or
// unknown ID is passed through so the handler produces its usual 400/404.
// Repositories without a recorded creator are admin-only.
func (m *AuthMiddleware) RequireRepoOwnerOrAdmin(param string) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := uuid.Parse(c.Param(param))
		if err != nil {
			c.Next()
			return
		}
		if m.db == nil {
			if canModifyRepo(c, nil) {
				c.Next()
			}
			return
		}
		var repo models.Repository
		lookupErr := m.db.WithContext(c.Request.Context()).Unscoped().
			Select("id", "created_by").First(&repo, "id = ?", id).Error
		if errors.Is(lookupErr, gorm.ErrRecordNotFound) {
			c.Next()
			return
		}
		if lookupErr != nil {
			response.InternalError(c, "Failed to authorize repository access")
			c.Abort()
			return
		}
		if !canModifyRepo(c, repo.CreatedBy) {
			return
		}
		c.Next()
	}
}

// RequireCloneJobOwnerOrAdmin is the clone-job counterpart of
// RequireRepoOwnerOrAdmin: the job's repository must belong to the caller
// (or the caller must be an admin).
func (m *AuthMiddleware) RequireCloneJobOwnerOrAdmin(param string) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := uuid.Parse(c.Param(param))
		if err != nil {
			c.Next()
			return
		}
		if m.db == nil {
			if canModifyRepo(c, nil) {
				c.Next()
			}
			return
		}
		var job models.CloneJob
		jobErr := m.db.WithContext(c.Request.Context()).
			Select("id", "repository_id").First(&job, "id = ?", id).Error
		if errors.Is(jobErr, gorm.ErrRecordNotFound) {
			c.Next()
			return
		}
		var repo models.Repository
		if jobErr == nil {
			jobErr = m.db.WithContext(c.Request.Context()).Unscoped().
				Select("id", "created_by").First(&repo, "id = ?", job.RepositoryID).Error
		}
		if jobErr != nil && !errors.Is(jobErr, gorm.ErrRecordNotFound) {
			response.InternalError(c, "Failed to authorize clone job access")
			c.Abort()
			return
		}
		if !canModifyRepo(c, repo.CreatedBy) {
			return
		}
		c.Next()
	}
}

// canModifyRepo reports whether the current user is an admin or the given
// repository creator. On denial it writes the response and aborts.
func canModifyRepo(c *gin.Context, createdBy *uuid.UUID) bool {
	user, ok := GetCurrentUser(c)
	if !ok {
		response.Unauthorized(c, "Authentication required")
		c.Abort()
		return false
	}
	if user.IsAdmin() || (createdBy != nil && *createdBy == user.ID) {
		return true
	}
	response.Forbidden(c, "Only the repository owner or an admin can modify this repository")
	c.Abort()
	return false
}
