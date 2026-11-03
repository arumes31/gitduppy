package config

import (
	"os"
	"path/filepath"
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

// writeConfig writes a YAML config file into a temp dir and returns its path.
func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSecurityDefaults(t *testing.T) {
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Security.PasswordMinLength != 8 {
		t.Errorf("default password_min_length = %d, want 8", cfg.Security.PasswordMinLength)
	}
	if cfg.Logging.AuditRetentionDays != 90 {
		t.Errorf("default audit_retention_days = %d, want 90", cfg.Logging.AuditRetentionDays)
	}
	if cfg.Email.AllowInsecure {
		t.Error("email.allow_insecure must default to false")
	}
}

func TestLoadPolicyAndRetentionFromFile(t *testing.T) {
	path := writeConfig(t, `
security:
  password_min_length: 14
  password_require_uppercase: true
  password_require_special: true
  rate_limit:
    auth_requests_per_minute: 3
logging:
  audit_retention_days: 0
`)
	cfg, err := load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Security.PasswordMinLength != 14 || !cfg.Security.PasswordRequireUppercase ||
		!cfg.Security.PasswordRequireSpecial || cfg.Security.PasswordRequireNumbers {
		t.Errorf("password policy not loaded: %+v", cfg.Security)
	}
	if cfg.Security.RateLimit.AuthRequestsPerMinute != 3 {
		t.Errorf("auth rate limit = %d, want 3", cfg.Security.RateLimit.AuthRequestsPerMinute)
	}
	if cfg.Logging.AuditRetentionDays != 0 {
		t.Errorf("audit_retention_days = %d, want 0 (keep forever)", cfg.Logging.AuditRetentionDays)
	}
	if len(cfg.Warnings) != 0 {
		t.Errorf("unexpected warnings: %v", cfg.Warnings)
	}
}

func TestLegacyTLSKeysAreMigrated(t *testing.T) {
	path := writeConfig(t, `
server:
  tls_enabled: false
  tls_cert: "/certs/server.crt"
  tls_key: "/certs/server.key"
email:
  smtp_username: "mailer"
`)
	cfg, err := load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Server.TLS.Cert != "/certs/server.crt" || cfg.Server.TLS.Key != "/certs/server.key" {
		t.Errorf("legacy TLS keys not migrated: %+v", cfg.Server.TLS)
	}
	if cfg.Email.SMTPUser != "mailer" {
		t.Errorf("legacy smtp_username not migrated: %q", cfg.Email.SMTPUser)
	}
	joined := strings.Join(cfg.Warnings, "\n")
	for _, key := range []string{"server.tls_enabled", "server.tls_cert", "server.tls_key", "email.smtp_username"} {
		if !strings.Contains(joined, key) {
			t.Errorf("missing deprecation warning for %s in %q", key, joined)
		}
	}
	if strings.Contains(joined, "unknown") {
		t.Errorf("legacy keys must not also be reported as unknown: %q", joined)
	}
}

func TestLegacyTLSEnabledWithoutCertFails(t *testing.T) {
	// Following the old flat example must not silently yield a plaintext
	// listener: tls_enabled is migrated, and a missing key pair is fatal.
	path := writeConfig(t, "server:\n  tls_enabled: true\n")
	if _, err := load(path); err == nil || !strings.Contains(err.Error(), "server.tls") {
		t.Fatalf("expected TLS configuration error, got %v", err)
	}
	path = writeConfig(t, "server:\n  tls:\n    enabled: true\n    cert: /nonexistent.crt\n    key: /nonexistent.key\n")
	if _, err := load(path); err == nil || !strings.Contains(err.Error(), "cannot be loaded") {
		t.Fatalf("expected unreadable key pair error, got %v", err)
	}
}

func TestUnknownKeysWarn(t *testing.T) {
	path := writeConfig(t, `
server:
  port: 9090
  prot: 1
git:
  clone_timeout: 300
`)
	cfg, err := load(path)
	if err != nil {
		t.Fatalf("unknown keys must not be fatal: %v", err)
	}
	joined := strings.Join(cfg.Warnings, "\n")
	for _, key := range []string{"server.prot", "git.clone_timeout"} {
		if !strings.Contains(joined, key) {
			t.Errorf("missing unknown-key warning for %s in %q", key, joined)
		}
	}
	if strings.Contains(joined, "server.port") {
		t.Errorf("known key reported as unknown: %q", joined)
	}
}

func TestValidateRejectsUnsafeValues(t *testing.T) {
	cases := map[string]string{
		"zero auth rate limit":  "security:\n  rate_limit:\n    auth_requests_per_minute: 0\n",
		"negative api limit":    "security:\n  rate_limit:\n    api_requests_per_minute: -1\n",
		"short password policy": "security:\n  password_min_length: 6\n",
		"negative retention":    "logging:\n  audit_retention_days: -1\n",
	}
	for name, body := range cases {
		if _, err := load(writeConfig(t, body)); err == nil {
			t.Errorf("%s: expected validation error", name)
		}
	}
}

// TestExampleConfigsUseSupportedKeys keeps the shipped example files honest:
// every key they advertise must be one the loader actually reads.
func TestExampleConfigsUseSupportedKeys(t *testing.T) {
	for _, rel := range []string{"../../config.example.yaml", "../../examples/config.yaml"} {
		cfg, err := load(rel)
		if err != nil {
			t.Errorf("%s: load: %v", rel, err)
			continue
		}
		if len(cfg.Warnings) != 0 {
			t.Errorf("%s advertises unsupported or deprecated keys:\n%s", rel, strings.Join(cfg.Warnings, "\n"))
		}
	}
}
