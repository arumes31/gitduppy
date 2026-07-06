package models

import (
	"time"

	"github.com/google/uuid"
)

// User represents an application user with authentication details.
type User struct {
	ID            uuid.UUID  `gorm:"type:uuid;primaryKey" json:"id"`
	Username      string     `gorm:"size:64;uniqueIndex;not null" json:"username"`
	Email         string     `gorm:"size:255;uniqueIndex;not null" json:"email"`
	PasswordHash  *string    `gorm:"size:255" json:"-"`
	Role          string     `gorm:"size:20;not null;default:user" json:"role"`
	IsActive      bool       `gorm:"default:true" json:"is_active"`
	// Explicit column names: GORM's default naming would map these to
	// "o_auth_provider"/"o_auth_subject", but the OAuth queries reference
	// "oauth_provider"/"oauth_subject", so pin the columns to match.
	OAuthProvider *string    `gorm:"size:50;column:oauth_provider" json:"oauth_provider,omitempty"`
	OAuthSubject  *string    `gorm:"size:255;column:oauth_subject" json:"-"`
	LastLogin     *time.Time `json:"last_login,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

// TableName specifies the table name for the User model.
func (User) TableName() string {
	return "users"
}

// IsAdmin returns true if the user has admin role.
func (u *User) IsAdmin() bool {
	return u.Role == "admin"
}
