package models

import (
	"time"

	"github.com/google/uuid"
)

// Tag represents a label that can be applied to repositories
type Tag struct {
	ID        uuid.UUID `gorm:"type:uuid;primaryKey" json:"id"`
	Name      string    `gorm:"size:50;uniqueIndex;not null" json:"name"`
	Color     string    `gorm:"size:7;default:#6366f1" json:"color"`
	CreatedAt time.Time `json:"created_at"`

	// Relations
	Repositories []Repository `gorm:"many2many:repository_tags" json:"-"`
}

// TableName specifies the table name for the Tag model
func (Tag) TableName() string {
	return "tags"
}

// RepositoryTag represents the many-to-many relationship between repositories and tags
type RepositoryTag struct {
	ID           uuid.UUID `gorm:"type:uuid;primaryKey" json:"id"`
	RepositoryID uuid.UUID `gorm:"type:uuid;not null;uniqueIndex:idx_repo_tag" json:"repository_id"`
	TagID        uuid.UUID `gorm:"type:uuid;not null;uniqueIndex:idx_repo_tag" json:"tag_id"`
	CreatedAt    time.Time `json:"created_at"`

	// Relations
	Repository *Repository `gorm:"foreignKey:RepositoryID" json:"-"`
	Tag        *Tag        `gorm:"foreignKey:TagID" json:"-"`
}

// TableName specifies the table name for the RepositoryTag model
func (RepositoryTag) TableName() string {
	return "repository_tags"
}
