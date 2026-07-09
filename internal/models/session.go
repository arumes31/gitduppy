package models

import (
	"time"

	"github.com/google/uuid"
)

// Session represents a user session for authentication.
type Session struct {
	// Token is the primary key, but it stores the SHA-256 (hex, 64 chars) of the
	// session token, not the raw token. The raw token lives only in the client's
	// cookie; hashing at rest means a database leak cannot yield usable session
	// cookies. Lookups hash the incoming cookie value before matching (see
	// crypto.HashToken). NOTE: because the stored value changed from the raw token
	// to its hash, any sessions created before this change stop matching and every
	// user must log in again once after deploy.
	Token  string    `gorm:"size:64;primaryKey" json:"token"`
	UserID uuid.UUID `gorm:"type:uuid;not null;index" json:"user_id"`
	Data   string    `gorm:"type:text;not null" json:"-"`
	Expiry time.Time `json:"expiry"`

	// Relations
	User *User `gorm:"foreignKey:UserID" json:"user,omitempty"`
}

// TableName specifies the table name for the Session model.
func (Session) TableName() string {
	return "sessions"
}
