package models

import (
	"time"

	"github.com/google/uuid"
)

// HealthCheck represents a health check result for external services
type HealthCheck struct {
	ID             uuid.UUID `gorm:"type:uuid;primaryKey" json:"id"`
	TargetURL      string    `gorm:"size:2048;not null" json:"target_url"`
	Status         string    `gorm:"size:20;not null" json:"status"`
	ResponseTimeMs *int      `json:"response_time_ms,omitempty"`
	ErrorMessage   *string   `gorm:"type:text" json:"error_message,omitempty"`
	CheckedAt      time.Time `json:"checked_at"`
}

// TableName specifies the table name for the HealthCheck model
func (HealthCheck) TableName() string {
	return "health_checks"
}
