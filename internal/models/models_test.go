package models

import "testing"

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
