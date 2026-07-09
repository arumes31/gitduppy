package models

import (
	"testing"

	"github.com/google/uuid"
)

func TestTagBeforeCreateGeneratesID(t *testing.T) {
	// The sync worker creates tags via GORM's FirstOrCreate, which builds the
	// row internally with no call site to set the ID. Without the hook every
	// new tag inserted the zero UUID and collided on tags_pkey after the first,
	// so verify successive zero-ID tags each get a distinct, non-nil key.
	var a, b Tag
	if err := a.BeforeCreate(nil); err != nil {
		t.Fatalf("BeforeCreate returned error: %v", err)
	}
	if err := b.BeforeCreate(nil); err != nil {
		t.Fatalf("BeforeCreate returned error: %v", err)
	}
	if a.ID == uuid.Nil || b.ID == uuid.Nil {
		t.Fatalf("BeforeCreate left a zero UUID: a=%s b=%s", a.ID, b.ID)
	}
	if a.ID == b.ID {
		t.Errorf("BeforeCreate produced duplicate IDs: %s", a.ID)
	}

	// An explicitly-assigned ID (the service-layer convention) must be kept.
	existing := uuid.New()
	tag := Tag{ID: existing}
	if err := tag.BeforeCreate(nil); err != nil {
		t.Fatalf("BeforeCreate returned error: %v", err)
	}
	if tag.ID != existing {
		t.Errorf("BeforeCreate overwrote an explicit ID: got %s, want %s", tag.ID, existing)
	}
}

func TestUserIsAdmin(t *testing.T) {
	if !(&User{Role: "admin"}).IsAdmin() {
		t.Error("admin role should be admin")
	}
	if (&User{Role: "user"}).IsAdmin() {
		t.Error("user role should not be admin")
	}
	if (&User{}).IsAdmin() {
		t.Error("empty role should not be admin")
	}
}

func TestTableNames(t *testing.T) {
	// Each entry pairs a model's actual TableName() against the expected value so
	// a wrong name is caught. (A map keyed by TableName() cannot do this: it would
	// silently collapse colliding/wrong names and only ever check for emptiness.)
	cases := []struct {
		got  string
		want string
	}{
		{User{}.TableName(), "users"},
		{Repository{}.TableName(), "repositories"},
		{Session{}.TableName(), "sessions"},
		{APIKey{}.TableName(), "api_keys"},
		{AuditLog{}.TableName(), "audit_logs"},
		{CloneJob{}.TableName(), "clone_jobs"},
		{CloneLog{}.TableName(), "clone_logs"},
		{Tag{}.TableName(), "tags"},
		{RepositoryTag{}.TableName(), "repository_tags"},
		{WebhookConfig{}.TableName(), "webhook_configs"},
		{WebhookDelivery{}.TableName(), "webhook_deliveries"},
		{HealthCheck{}.TableName(), "health_checks"},
		{DeletedBranch{}.TableName(), "deleted_branches"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("TableName() = %q, want %q", c.got, c.want)
		}
	}
}
