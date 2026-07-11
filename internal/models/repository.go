package models

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Repository represents a git repository mirror configuration.
type Repository struct {
	ID                   uuid.UUID `gorm:"type:uuid;primaryKey" json:"id"`
	Name                 string    `gorm:"size:255;not null" json:"name"`
	URL                  string    `gorm:"size:2048;not null" json:"url"`
	Branch               string    `gorm:"size:255;default:main" json:"branch"`
	AuthType             string    `gorm:"size:20;not null;default:none" json:"auth_type"`
	EncryptedCredentials string    `gorm:"type:text" json:"-"` // Never expose in JSON
	StoragePath          string    `gorm:"size:1024;not null" json:"storage_path"`
	Status               string    `gorm:"size:20;not null;default:pending" json:"status"`
	IsBare               bool      `gorm:"default:false" json:"is_bare"`
	LFSEnabled           bool      `gorm:"default:false" json:"lfs_enabled"`
	MirrorIssues         bool      `gorm:"default:false" json:"mirror_issues"`
	MirrorPullRequests   bool      `gorm:"default:false" json:"mirror_pull_requests"`
	MirrorReleases       bool      `gorm:"default:false" json:"mirror_releases"`
	MirrorWiki           bool      `gorm:"default:false" json:"mirror_wiki"`
	IsActive             bool      `gorm:"default:true" json:"is_active"`
	Visibility           string    `gorm:"size:20" json:"visibility,omitempty"` // "public", "private", or "" if unknown
	// SizeBytes is the repository's on-disk size in bytes, recomputed and persisted
	// after each successful clone/fetch. It lets the dashboard sum storage usage
	// from the database instead of walking the whole storage tree. Repositories
	// cloned before this field existed (or not yet re-synced) carry 0 until their
	// next successful sync.
	SizeBytes            int64      `gorm:"default:0" json:"size_bytes"`
	CloneIntervalMinutes int        `gorm:"default:60" json:"clone_interval_minutes"`
	RetentionDays        int        `gorm:"default:30" json:"retention_days"`
	Description          *string    `gorm:"type:text" json:"description,omitempty"`
	CreatedBy            *uuid.UUID `gorm:"type:uuid" json:"created_by,omitempty"`
	LastCloneAt          *time.Time `json:"last_clone_at,omitempty"`
	LastCloneStatus      *string    `gorm:"size:20" json:"last_clone_status,omitempty"`
	RetryCount           int        `gorm:"default:0" json:"retry_count"`
	MaxRetries           int        `gorm:"default:3" json:"max_retries"`
	// NextRetryAt is the earliest time the scheduler may re-attempt a repository
	// whose last clone failed. It implements exponential backoff with jitter so a
	// hard-failing repo is not re-queued on every scheduler tick. It is set on
	// failure and cleared on a successful clone.
	NextRetryAt *time.Time     `json:"next_retry_at,omitempty"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	DeletedAt   gorm.DeletedAt `gorm:"index" json:"deleted_at,omitempty"`

	// Relations
	CreatedByUser *User      `gorm:"foreignKey:CreatedBy" json:"created_by_user,omitempty"`
	Tags          []Tag      `gorm:"many2many:repository_tags" json:"tags,omitempty"`
	CloneJobs     []CloneJob `gorm:"foreignKey:RepositoryID" json:"-"`
}

// TableName specifies the table name for the Repository model.
func (Repository) TableName() string {
	return "repositories"
}

// BeforeCreate assigns a UUID primary key when one was not set explicitly (same
// rationale as Tag.BeforeCreate).
func (r *Repository) BeforeCreate(*gorm.DB) error {
	if r.ID == uuid.Nil {
		r.ID = uuid.New()
	}
	return nil
}
