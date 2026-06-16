package models

import (
	"time"

	"github.com/google/uuid"
)

// Session represents a user session for authentication
type Session struct {
	Token  string    `gorm:"size:43;primaryKey" json:"token"`
	UserID uuid.UUID `gorm:"type:uuid;not null;index" json:"user_id"`
	Data   string    `gorm:"type:text;not null" json:"-"`
	Expiry time.Time `json:"expiry"`

	// Relations
	User *User `gorm:"foreignKey:UserID" json:"user,omitempty"`
}

// TableName specifies the table name for the Session model
func (Session) TableName() string {
	return "sessions"
}
