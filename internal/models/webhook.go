package models

import (
	"time"

	"github.com/google/uuid"
)

// WebhookConfig represents a webhook configuration for sending notifications
type WebhookConfig struct {
	ID             uuid.UUID  `gorm:"type:uuid;primaryKey" json:"id"`
	UserID         uuid.UUID  `gorm:"type:uuid;not null;index" json:"user_id"`
	RepositoryID   *uuid.UUID `gorm:"type:uuid;index" json:"repository_id,omitempty"`
	Name           string     `gorm:"size:100;not null" json:"name"`
	URL            string     `gorm:"size:2048;not null" json:"url"`
	Secret         string     `gorm:"size:255" json:"-"`
	Events         []string   `gorm:"type:jsonb;not null;default:'[]'" json:"events"`
	IsActive       bool       `gorm:"default:true" json:"is_active"`
	RetryCount     int        `gorm:"default:3" json:"retry_count"`
	TimeoutSeconds int        `gorm:"default:30" json:"timeout_seconds"`
	Provider       string     `gorm:"size:50;not null;default:'generic'" json:"provider"`
	URLPattern     string     `gorm:"size:2048" json:"url_pattern"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`

	// Relations
	User       *User             `gorm:"foreignKey:UserID" json:"user,omitempty"`
	Repository *Repository       `gorm:"foreignKey:RepositoryID" json:"repository,omitempty"`
	Deliveries []WebhookDelivery `gorm:"foreignKey:WebhookConfigID" json:"-"`
}

// TableName specifies the table name for the WebhookConfig model
func (WebhookConfig) TableName() string {
	return "webhook_configs"
}

// WebhookDelivery represents a single webhook delivery attempt
type WebhookDelivery struct {
	ID              uuid.UUID `gorm:"type:uuid;primaryKey" json:"id"`
	WebhookConfigID uuid.UUID `gorm:"type:uuid;not null;index" json:"webhook_config_id"`
	EventType       string    `gorm:"size:100;not null" json:"event_type"`
	Payload         string    `gorm:"type:text;not null" json:"payload"`
	HTTPStatus      *int      `json:"http_status,omitempty"`
	ResponseBody    string    `gorm:"type:text" json:"response_body,omitempty"`
	Success         bool      `gorm:"default:false" json:"success"`
	AttemptNumber   int       `gorm:"default:1" json:"attempt_number"`
	DeliveredAt     time.Time `json:"delivered_at"`

	// Relations
	WebhookConfig *WebhookConfig `gorm:"foreignKey:WebhookConfigID" json:"-"`
}

// TableName specifies the table name for the WebhookDelivery model
func (WebhookDelivery) TableName() string {
	return "webhook_deliveries"
}
