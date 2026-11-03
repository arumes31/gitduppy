package models

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

const (
	// LegacyDeletedBranchRefPrefix is the archival ref namespace used before
	// archival refs were made unique per deletion event. Such refs were derived
	// from the branch name alone, so two deletions of the same branch shared one
	// ref. Rows written before the change (ArchiveRef == "") still resolve here.
	LegacyDeletedBranchRefPrefix = "refs/paperbin/heads/"

	// DeletedBranchRefPrefix is the namespace for per-event archival refs. The
	// ref name is this prefix plus the DeletedBranch row ID, so it is unique per
	// deletion event and never embeds the upstream-controlled branch name.
	DeletedBranchRefPrefix = "refs/paperbin/events/"
)

// DeletedBranch represents a branch that was pruned (deleted online) but is kept in the paperbin.
type DeletedBranch struct {
	ID           uuid.UUID `gorm:"type:uuid;primaryKey" json:"id"`
	RepositoryID uuid.UUID `gorm:"type:uuid;index;not null" json:"repository_id"`
	BranchName   string    `gorm:"size:255;not null" json:"branch_name"`
	CommitSHA    string    `gorm:"size:64;not null" json:"commit_sha"`
	// ArchiveRef is the git ref that keeps CommitSHA reachable for this deletion
	// event (DeletedBranchRefPrefix + ID). Empty for legacy rows, which fall back
	// to the shared branch-name-derived ref (see ArchivalRef).
	ArchiveRef string    `gorm:"size:1024;not null;default:''" json:"-"`
	DeletedAt  time.Time `json:"deleted_at"`

	// Relation
	Repository *Repository `gorm:"foreignKey:RepositoryID" json:"repository,omitempty"`
}

// TableName specifies the table name for the DeletedBranch model.
func (DeletedBranch) TableName() string {
	return "deleted_branches"
}

// BeforeCreate assigns a UUID primary key when one was not set explicitly (same
// rationale as Tag.BeforeCreate).
func (b *DeletedBranch) BeforeCreate(*gorm.DB) error {
	if b.ID == uuid.Nil {
		b.ID = uuid.New()
	}
	return nil
}

// DeletedBranchEventRef returns the per-event archival ref name for a deletion
// event with the given ID.
func DeletedBranchEventRef(id uuid.UUID) string {
	return DeletedBranchRefPrefix + id.String()
}

// ArchivalRef returns the git ref protecting this deletion event's commit:
// the persisted per-event ref, or the legacy branch-name-derived ref for rows
// created before per-event refs existed.
func (b *DeletedBranch) ArchivalRef() string {
	if b.ArchiveRef != "" {
		return b.ArchiveRef
	}
	return LegacyDeletedBranchRefPrefix + b.BranchName
}

// HasLegacyArchivalRef reports whether this row relies on the shared legacy ref.
func (b *DeletedBranch) HasLegacyArchivalRef() bool {
	return b.ArchiveRef == ""
}
