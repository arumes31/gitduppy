package models

import (
	"time"

	"github.com/google/uuid"
)

// CloneJob represents a single clone/fetch operation for a repository
type CloneJob struct {
	ID              uuid.UUID  `gorm:"type:uuid;primaryKey" json:"id"`
	RepositoryID    uuid.UUID  `gorm:"type:uuid;not null;index" json:"repository_id"`
	TriggerType     string     `gorm:"size:20;not null" json:"trigger_type"`
	Status          string     `gorm:"size:20;not null;default:pending" json:"status"`
	OutputLog       string     `gorm:"type:text" json:"output_log,omitempty"`
	ExitCode        *int       `json:"exit_code,omitempty"`
	ProgressPercent int        `gorm:"default:0" json:"progress_percent"`
	StartedAt       *time.Time `json:"started_at,omitempty"`
	CompletedAt     *time.Time `json:"completed_at,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`

	// Relations
	Repository *Repository `gorm:"foreignKey:RepositoryID" json:"repository,omitempty"`
	CloneLogs  []CloneLog  `gorm:"foreignKey:CloneJobID" json:"logs,omitempty"`
}

// TableName specifies the table name for the CloneJob model
func (CloneJob) TableName() string {
	return "clone_jobs"
}
