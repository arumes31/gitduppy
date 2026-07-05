package models

import (
	"time"

	"github.com/google/uuid"
)

// DeletedBranch represents a branch that was pruned (deleted online) but is kept in the paperbin.
type DeletedBranch struct {
	ID           uuid.UUID `gorm:"type:uuid;primaryKey" json:"id"`
	RepositoryID uuid.UUID `gorm:"type:uuid;index;not null" json:"repository_id"`
	BranchName   string    `gorm:"size:255;not null" json:"branch_name"`
	CommitSHA    string    `gorm:"size:64;not null" json:"commit_sha"`
	DeletedAt    time.Time `json:"deleted_at"`

	// Relation
	Repository *Repository `gorm:"foreignKey:RepositoryID" json:"repository,omitempty"`
}

// TableName specifies the table name for the DeletedBranch model.
func (DeletedBranch) TableName() string {
	return "deleted_branches"
}
