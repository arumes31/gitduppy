package config

import (
	"strings"
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Server.Port != 8080 {
		t.Errorf("default port = %d, want 8080", cfg.Server.Port)
	}
	if cfg.Database.Port != 5432 {
		t.Errorf("default db port = %d, want 5432", cfg.Database.Port)
	}
	if cfg.Worker.MaxConcurrent != 3 {
		t.Errorf("default max_concurrent = %d, want 3", cfg.Worker.MaxConcurrent)
	}
	if cfg.Server.ReadTimeout != 30*time.Second {
		t.Errorf("default read_timeout = %v, want 30s", cfg.Server.ReadTimeout)
	}
}

// TestEnvBindingForKeysWithoutDefaults is the regression test for the viper
// AutomaticEnv+Unmarshal fix: security keys and other keys that have no default
// value must still be populated from GITMIRRORS_* environment variables.
func TestEnvBindingForKeysWithoutDefaults(t *testing.T) {
	const key = "0123456789abcdef0123456789abcdef"
	t.Setenv("GITMIRRORS_SECURITY_MASTER_KEY", key)
	t.Setenv("GITMIRRORS_SECURITY_SESSION_SECRET", "sess")
	t.Setenv("GITMIRRORS_SECURITY_CSRF_KEY", "csrf")
	t.Setenv("GITMIRRORS_OAUTH_GITHUB_CLIENT_ID", "gh-client")
	t.Setenv("GITMIRRORS_EMAIL_SMTP_HOST", "smtp.local")
	t.Setenv("GITMIRRORS_DATABASE_HOST", "db.example")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Security.MasterKey != key {
		t.Errorf("master_key not bound from env: got %q", cfg.Security.MasterKey)
	}
	if cfg.Security.SessionSecret != "sess" {
		t.Errorf("session_secret not bound: got %q", cfg.Security.SessionSecret)
	}
	if cfg.Security.CSRFKey != "csrf" {
		t.Errorf("csrf_key not bound: got %q", cfg.Security.CSRFKey)
	}
	if cfg.OAuth.GitHub.ClientID != "gh-client" {
		t.Errorf("oauth client id not bound: got %q", cfg.OAuth.GitHub.ClientID)
	}
	if cfg.Email.SMTPHost != "smtp.local" {
		t.Errorf("smtp host not bound: got %q", cfg.Email.SMTPHost)
	}
	if cfg.Database.Host != "db.example" {
		t.Errorf("db host not bound: got %q", cfg.Database.Host)
	}
}

func TestDSN(t *testing.T) {
	c := DatabaseConfig{Host: "h", Port: 5432, User: "u", Password: "p", Name: "db", SSLMode: "disable"}
	got := c.DSN()
	want := "host=h port=5432 user=u password=p dbname=db sslmode=disable"
	if got != want {
		t.Errorf("DSN()=%q want %q", got, want)
	}
}

func TestCheckMasterKey(t *testing.T) {
	bad := []string{
		"your-32-byte-long-encryption-key",
		"your-32-byte-encryption-key-here",
		"YOUR-32-byte-long-encryption-key",
		strings.Repeat("k", 32),
		"abababababababababababababababab",
		"please-changeme-please-changeme!",
	}
	for _, k := range bad {
		if err := CheckMasterKey(k); err == nil {
			t.Errorf("CheckMasterKey(%q) = nil, want error", k)
		}
	}
	good := []string{
		"3f9a1c7e5b2d8046af13ce97b5d20e68",
		"6b1f0e4c9a2d7358e0f1a2b3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d5e6f7",
	}
	for _, k := range good {
		if err := CheckMasterKey(k); err != nil {
			t.Errorf("CheckMasterKey(%q) = %v, want nil", k, err)
		}
	}
}
