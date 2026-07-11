package models

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// User represents an application user with authentication details.
type User struct {
	ID           uuid.UUID `gorm:"type:uuid;primaryKey" json:"id"`
	Username     string    `gorm:"size:64;uniqueIndex;not null" json:"username"`
	Email        string    `gorm:"size:255;uniqueIndex;not null" json:"email"`
	PasswordHash *string   `gorm:"size:255" json:"-"`
	Role         string    `gorm:"size:20;not null;default:user" json:"role"`
	IsActive     bool      `gorm:"default:true" json:"is_active"`
	// Explicit column names: GORM's default naming would map these to
	// "o_auth_provider"/"o_auth_subject", but the OAuth queries reference
	// "oauth_provider"/"oauth_subject", so pin the columns to match.
	OAuthProvider *string    `gorm:"size:50;column:oauth_provider" json:"oauth_provider,omitempty"`
	OAuthSubject  *string    `gorm:"size:255;column:oauth_subject" json:"-"`
	LastLogin     *time.Time `json:"last_login,omitempty"`
	// FailedLoginAttempts counts consecutive failed password logins; it is reset
	// to zero on any successful login. The zero value (no failures) is the default
	// for both new and pre-existing rows, so no data migration is needed.
	FailedLoginAttempts int `gorm:"not null;default:0" json:"-"`
	// LockedUntil, when non-nil and in the future, blocks password logins until it
	// elapses (progressive lockout after repeated failures). Nil means not locked;
	// it is cleared on a successful login.
	LockedUntil *time.Time `json:"-"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// TableName specifies the table name for the User model.
func (User) TableName() string {
	return "users"
}

// BeforeCreate assigns a UUID primary key when one was not set explicitly (same
// rationale as Tag.BeforeCreate: GORM's create helpers can build rows with no
// call site to set the ID).
func (u *User) BeforeCreate(*gorm.DB) error {
	if u.ID == uuid.Nil {
		u.ID = uuid.New()
	}
	return nil
}

// IsAdmin returns true if the user has admin role.
func (u *User) IsAdmin() bool {
	return u.Role == "admin"
}
