package models

import (
	"time"

	"github.com/google/uuid"
)

// APIKey represents an API key for programmatic access
type APIKey struct {
	ID         uuid.UUID  `gorm:"type:uuid;primaryKey" json:"id"`
	UserID     uuid.UUID  `gorm:"type:uuid;not null;index" json:"user_id"`
	KeyHash    string     `gorm:"size:64;not null;index" json:"-"`
	KeyPrefix  string     `gorm:"size:8;not null" json:"key_prefix"`
	Name       string     `gorm:"size:100;not null" json:"name"`
	IsActive   bool       `gorm:"default:true" json:"is_active"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`

	// Relations
	User *User `gorm:"foreignKey:UserID" json:"user,omitempty"`
}

// TableName specifies the table name for the APIKey model
func (APIKey) TableName() string {
	return "api_keys"
}
