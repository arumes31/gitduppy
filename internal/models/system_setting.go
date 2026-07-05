package models

import (
	"time"

	"github.com/google/uuid"
)

// SystemSetting represents a dynamic configuration setting stored in the database.
type SystemSetting struct {
	ID          uuid.UUID `gorm:"type:uuid;primary_key" json:"id"`
	Key         string    `gorm:"type:varchar(255);uniqueIndex;not null" json:"key"`
	Value       string    `gorm:"type:text;not null" json:"value"`
	IsEncrypted bool      `gorm:"default:false" json:"is_encrypted"`
	Description string    `gorm:"type:text" json:"description"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}
