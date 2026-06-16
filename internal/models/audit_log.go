package models

import (
	"time"

	"github.com/google/uuid"
)

// AuditLog represents an audit log entry for tracking user actions
type AuditLog struct {
	ID           uuid.UUID  `gorm:"type:uuid;primaryKey" json:"id"`
	UserID       *uuid.UUID `gorm:"type:uuid;index" json:"user_id,omitempty"`
	RepositoryID *uuid.UUID `gorm:"type:uuid;index" json:"repository_id,omitempty"`
	Action       string     `gorm:"size:100;not null" json:"action"`
	Details      string     `gorm:"type:text" json:"details,omitempty"`
	IPAddress    *string    `gorm:"size:45" json:"ip_address,omitempty"`
	UserAgent    *string    `gorm:"type:text" json:"user_agent,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`

	// Relations
	User       *User       `gorm:"foreignKey:UserID" json:"user,omitempty"`
	Repository *Repository `gorm:"foreignKey:RepositoryID" json:"repository,omitempty"`
}

// TableName specifies the table name for the AuditLog model
func (AuditLog) TableName() string {
	return "audit_logs"
}
