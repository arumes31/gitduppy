package models

import (
	"encoding/json"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Repository represents a git repository mirror configuration.
type Repository struct {
	ID                   uuid.UUID `gorm:"type:uuid;primaryKey" json:"id"`
	Name                 string    `gorm:"size:255;not null" json:"name"`
	URL                  string    `gorm:"size:2048;not null" json:"url"`
	Branch               string    `gorm:"size:255;default:main" json:"branch"`
	AuthType             string    `gorm:"size:20;not null;default:none" json:"auth_type"`
	EncryptedCredentials string    `gorm:"type:text" json:"-"` // Never expose in JSON
	StoragePath          string    `gorm:"size:1024;not null" json:"storage_path"`
	Status               string    `gorm:"size:20;not null;default:pending" json:"status"`
	IsBare               bool      `gorm:"default:false" json:"is_bare"`
	LFSEnabled           bool      `gorm:"default:false" json:"lfs_enabled"`
	MirrorIssues         bool      `gorm:"default:false" json:"mirror_issues"`
	MirrorPullRequests   bool      `gorm:"default:false" json:"mirror_pull_requests"`
	MirrorReleases       bool      `gorm:"default:false" json:"mirror_releases"`
	MirrorWiki           bool      `gorm:"default:false" json:"mirror_wiki"`
	IsActive             bool      `gorm:"default:true" json:"is_active"`
	Visibility           string    `gorm:"size:20" json:"visibility,omitempty"` // "public", "private", or "" if unknown
	// SizeBytes is the repository's on-disk size in bytes, recomputed and persisted
	// after each successful clone/fetch. It lets the dashboard sum storage usage
	// from the database instead of walking the whole storage tree. Repositories
	// cloned before this field existed (or not yet re-synced) carry 0 until their
	// next successful sync.
	SizeBytes            int64      `gorm:"default:0" json:"size_bytes"`
	CloneIntervalMinutes int        `gorm:"default:60" json:"clone_interval_minutes"`
	RetentionDays        int        `gorm:"default:30" json:"retention_days"`
	Description          *string    `gorm:"type:text" json:"description,omitempty"`
	CreatedBy            *uuid.UUID `gorm:"type:uuid" json:"created_by,omitempty"`
	LastCloneAt          *time.Time `json:"last_clone_at,omitempty"`
	LastCloneStatus      *string    `gorm:"size:20" json:"last_clone_status,omitempty"`
	RetryCount           int        `gorm:"default:0" json:"retry_count"`
	MaxRetries           int        `gorm:"default:3" json:"max_retries"`
	// NextRetryAt is the earliest time the scheduler may re-attempt a repository
	// whose last clone failed. It implements exponential backoff with jitter so a
	// hard-failing repo is not re-queued on every scheduler tick. It is set on
	// failure and cleared on a successful clone.
	NextRetryAt *time.Time     `json:"next_retry_at,omitempty"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	DeletedAt   gorm.DeletedAt `gorm:"index" json:"deleted_at,omitempty"`

	// Relations
	CreatedByUser *User      `gorm:"foreignKey:CreatedBy" json:"created_by_user,omitempty"`
	Tags          []Tag      `gorm:"many2many:repository_tags" json:"tags,omitempty"`
	CloneJobs     []CloneJob `gorm:"foreignKey:RepositoryID" json:"-"`
}

// TableName specifies the table name for the Repository model.
func (Repository) TableName() string {
	return "repositories"
}

// BeforeCreate assigns a UUID primary key when one was not set explicitly (same
// rationale as Tag.BeforeCreate).
func (r *Repository) BeforeCreate(*gorm.DB) error {
	if r.ID == uuid.Nil {
		r.ID = uuid.New()
	}
	return nil
}

// MarshalJSON serializes the repository with any credentials embedded in the
// remote URL's userinfo removed (see RedactURLCredentials). Every API response
// that carries a Repository — directly, in a list, or preloaded onto a clone
// job or paperbin entry — goes through here, so a legacy row whose URL still
// holds https://user:token@host/... never leaks the secret to readers. The
// in-memory struct is left untouched, so clone/fetch code keeps using the real
// URL.
func (r Repository) MarshalJSON() ([]byte, error) {
	type repositoryJSON Repository // drops the method set, avoiding recursion
	out := repositoryJSON(r)
	out.URL = RedactURLCredentials(r.URL)
	return json.Marshal(out)
}

// RedactURLCredentials strips secrets from a git remote URL. For http(s)/git
// URLs the whole userinfo is removed, since a bare "username" there is often a
// token; for ssh:// URLs only the password is dropped and the login user is
// kept. A query string or fragment (e.g. ?token=...) is dropped as well, since
// git remotes never need one. scp-like remotes (git@host:path) carry no secret
// and are returned as is.
func RedactURLCredentials(raw string) string {
	if !strings.Contains(raw, "://") {
		return raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		// Unparseable: strip anything between "://" and the last "@" that
		// precedes the first path separator, so a malformed URL cannot smuggle
		// a credential past redaction.
		scheme, rest, _ := strings.Cut(raw, "://")
		authority, path, hasPath := strings.Cut(rest, "/")
		if i := strings.LastIndex(authority, "@"); i >= 0 {
			authority = authority[i+1:]
		}
		if hasPath {
			return scheme + "://" + authority + "/" + path
		}
		return scheme + "://" + authority
	}
	if u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawFragment != "" {
		u.RawQuery, u.ForceQuery, u.Fragment, u.RawFragment = "", false, "", ""
		if u.User == nil {
			return u.String()
		}
	}
	if u.User == nil {
		return raw
	}
	if strings.EqualFold(u.Scheme, "ssh") {
		if _, hasPassword := u.User.Password(); !hasPassword {
			return raw
		}
		u.User = url.User(u.User.Username())
	} else {
		u.User = nil
	}
	return u.String()
}

// URLHasCredentials reports whether a git remote URL embeds a secret that
// RedactURLCredentials would remove.
func URLHasCredentials(raw string) bool {
	return RedactURLCredentials(raw) != raw
}
