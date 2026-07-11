package models

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// DefaultTagColor is the default color assigned to new tags. It is referenced
// by the GORM default tag on Tag.Color and by the post-clone topic-tag creator
// so both paths stay in sync.
const DefaultTagColor = "#6366f1"

// Tag represents a label that can be applied to repositories.
type Tag struct {
	ID        uuid.UUID `gorm:"type:uuid;primaryKey" json:"id"`
	Name      string    `gorm:"size:50;uniqueIndex;not null" json:"name"`
	Color     string    `gorm:"size:7;default:#6366f1" json:"color"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	// Relations
	Repositories []Repository `gorm:"many2many:repository_tags" json:"-"`
}

// TableName specifies the table name for the Tag model.
func (Tag) TableName() string {
	return "tags"
}

// BeforeCreate generates a primary key when one was not set explicitly. Most
// callers assign uuid.New() themselves, but GORM's create-managing helpers
// (e.g. FirstOrCreate in the sync worker) build the row internally with no
// call site to set the ID, which would otherwise insert the zero UUID and
// collide on tags_pkey for every tag after the first.
func (t *Tag) BeforeCreate(*gorm.DB) error {
	if t.ID == uuid.Nil {
		t.ID = uuid.New()
	}
	return nil
}

// RepositoryTag represents the many-to-many relationship between repositories and tags.
type RepositoryTag struct {
	ID           uuid.UUID `gorm:"type:uuid;primaryKey" json:"id"`
	RepositoryID uuid.UUID `gorm:"type:uuid;not null;uniqueIndex:idx_repo_tag" json:"repository_id"`
	TagID        uuid.UUID `gorm:"type:uuid;not null;uniqueIndex:idx_repo_tag" json:"tag_id"`
	CreatedAt    time.Time `json:"created_at"`

	// Relations
	Repository *Repository `gorm:"foreignKey:RepositoryID" json:"-"`
	Tag        *Tag        `gorm:"foreignKey:TagID" json:"-"`
}

// TableName specifies the table name for the RepositoryTag model.
func (RepositoryTag) TableName() string {
	return "repository_tags"
}

// BeforeCreate assigns a UUID primary key when one was not set explicitly (same
// rationale as Tag.BeforeCreate). This is what makes association appends/replaces
// through the explicit join model (see database.SetupJoinTable) insert a valid PK.
func (rt *RepositoryTag) BeforeCreate(*gorm.DB) error {
	if rt.ID == uuid.Nil {
		rt.ID = uuid.New()
	}
	return nil
}
