package models

import (
	"time"

	"github.com/google/uuid"
)

// Repository represents a git repository mirror configuration.
type Repository struct {
	ID                   uuid.UUID  `gorm:"type:uuid;primaryKey" json:"id"`
	Name                 string     `gorm:"size:255;not null" json:"name"`
	URL                  string     `gorm:"size:2048;not null" json:"url"`
	Branch               string     `gorm:"size:255;default:main" json:"branch"`
	AuthType             string     `gorm:"size:20;not null;default:none" json:"auth_type"`
	EncryptedCredentials string     `gorm:"type:text" json:"-"` // Never expose in JSON
	StoragePath          string     `gorm:"size:1024;not null" json:"storage_path"`
	Status               string     `gorm:"size:20;not null;default:pending" json:"status"`
	IsBare               bool       `gorm:"default:false" json:"is_bare"`
	LFSEnabled           bool       `gorm:"default:false" json:"lfs_enabled"`
	IsActive             bool       `gorm:"default:true" json:"is_active"`
	CloneIntervalMinutes int        `gorm:"default:60" json:"clone_interval_minutes"`
	Description          *string    `gorm:"type:text" json:"description,omitempty"`
	CreatedBy            *uuid.UUID `gorm:"type:uuid" json:"created_by,omitempty"`
	LastCloneAt          *time.Time `json:"last_clone_at,omitempty"`
	LastCloneStatus      *string    `gorm:"size:20" json:"last_clone_status,omitempty"`
	RetryCount           int        `gorm:"default:0" json:"retry_count"`
	MaxRetries           int        `gorm:"default:3" json:"max_retries"`
	CreatedAt            time.Time  `json:"created_at"`
	UpdatedAt            time.Time  `json:"updated_at"`

	// Relations
	CreatedByUser *User      `gorm:"foreignKey:CreatedBy" json:"created_by_user,omitempty"`
	Tags          []Tag      `gorm:"many2many:repository_tags" json:"tags,omitempty"`
	CloneJobs     []CloneJob `gorm:"foreignKey:RepositoryID" json:"-"`
}

// TableName specifies the table name for the Repository model.
func (Repository) TableName() string {
	return "repositories"
}
