package models

import (
	"time"

	"github.com/google/uuid"
)

// CloneLog represents a detailed log entry for a clone job
type CloneLog struct {
	ID         uuid.UUID `gorm:"type:uuid;primaryKey" json:"id"`
	CloneJobID uuid.UUID `gorm:"type:uuid;not null;index" json:"clone_job_id"`
	LogLevel   string    `gorm:"size:10;not null" json:"log_level"`
	Message    string    `gorm:"type:text;not null" json:"message"`
	CreatedAt  time.Time `json:"created_at"`

	// Relations
	CloneJob *CloneJob `gorm:"foreignKey:CloneJobID" json:"-"`
}

// TableName specifies the table name for the CloneLog model
func (CloneLog) TableName() string {
	return "clone_logs"
}
