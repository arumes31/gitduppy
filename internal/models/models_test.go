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
	cases := map[string]string{
		User{}.TableName():            "users",
		Repository{}.TableName():      "repositories",
		Session{}.TableName():         "sessions",
		APIKey{}.TableName():          "api_keys",
		AuditLog{}.TableName():        "audit_logs",
		CloneJob{}.TableName():        "clone_jobs",
		CloneLog{}.TableName():        "clone_logs",
		Tag{}.TableName():             "tags",
		RepositoryTag{}.TableName():   "repository_tags",
		WebhookConfig{}.TableName():   "webhook_configs",
		WebhookDelivery{}.TableName(): "webhook_deliveries",
		HealthCheck{}.TableName():     "health_checks",
		DeletedBranch{}.TableName():   "deleted_branches",
	}
	for got, want := range cases {
		if got == "" {
			t.Errorf("empty table name (want %q)", want)
		}
	}
}
