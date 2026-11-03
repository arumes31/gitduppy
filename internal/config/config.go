package config

import (
	"crypto/tls"
	"errors"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/spf13/viper"
)

// Config holds all application configuration.
type Config struct {
	Server     ServerConfig     `mapstructure:"server"`
	Database   DatabaseConfig   `mapstructure:"database"`
	Security   SecurityConfig   `mapstructure:"security"`
	OAuth      OAuthConfig      `mapstructure:"oauth"`
	Email      EmailConfig      `mapstructure:"email"`
	Worker     WorkerConfig     `mapstructure:"worker"`
	Logging    LoggingConfig    `mapstructure:"logging"`
	Storage    StorageConfig    `mapstructure:"storage"`
	Monitoring MonitoringConfig `mapstructure:"monitoring"`

	// Warnings collects non-fatal problems found while loading (deprecated or
	// unknown keys). Load runs before the logger exists, so main logs them.
	Warnings []string `mapstructure:"-" json:"-"`
}

// ServerConfig holds HTTP server configuration.
type ServerConfig struct {
	Port           int           `mapstructure:"port"`
	Host           string        `mapstructure:"host"`
	ReadTimeout    time.Duration `mapstructure:"read_timeout"`
	WriteTimeout   time.Duration `mapstructure:"write_timeout"`
	IdleTimeout    time.Duration `mapstructure:"idle_timeout"`
	TLS            TLSConfig     `mapstructure:"tls"`
	TrustedProxies []string      `mapstructure:"trusted_proxies"`
	BaseURL        string        `mapstructure:"base_url"`
}

// TLSConfig holds TLS/SSL configuration.
type TLSConfig struct {
	Enabled bool   `mapstructure:"enabled"`
	Cert    string `mapstructure:"cert"`
	Key     string `mapstructure:"key"`
}

// DatabaseConfig holds PostgreSQL connection configuration.
type DatabaseConfig struct {
	Host            string        `mapstructure:"host"`
	Port            int           `mapstructure:"port"`
	Name            string        `mapstructure:"name"`
	User            string        `mapstructure:"user"`
	Password        string        `mapstructure:"password"`
	SSLMode         string        `mapstructure:"sslmode"`
	MaxOpenConns    int           `mapstructure:"max_open_conns"`
	MaxIdleConns    int           `mapstructure:"max_idle_conns"`
	ConnMaxLifetime time.Duration `mapstructure:"conn_max_lifetime"`
}

// DSN returns the PostgreSQL connection string.
func (c *DatabaseConfig) DSN() string {
	return fmt.Sprintf(
		"host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		c.Host, c.Port, c.User, c.Password, c.Name, c.SSLMode,
	)
}

// SecurityConfig holds security-related configuration.
type SecurityConfig struct {
	MasterKey       string          `mapstructure:"master_key"`
	SessionSecret   string          `mapstructure:"session_secret"`
	CSRFKey         string          `mapstructure:"csrf_key"`
	SessionDuration time.Duration   `mapstructure:"session_duration"`
	RateLimit       RateLimitConfig `mapstructure:"rate_limit"`
	HSTSMaxAge      int             `mapstructure:"hsts_max_age"` // HSTS max age in seconds.
	// CookieSecure forces the Secure flag on the session cookie. When it is not
	// set explicitly (config file or GITMIRRORS_SECURITY_COOKIE_SECURE) it is
	// derived at load time from server.base_url — true for an https:// URL, false
	// otherwise — so HTTPS deployments get Secure cookies without extra config.
	CookieSecure bool `mapstructure:"cookie_secure"`
	// AllowInsecureMasterKey lets the server start with a known placeholder or
	// trivially weak master key (see CheckMasterKey). Only for existing
	// deployments that must keep decrypting credentials while they rotate.
	AllowInsecureMasterKey bool `mapstructure:"allow_insecure_master_key"`
	// AllowPrivateNetworkTargets lets outbound webhooks reach loopback and
	// private-network addresses, and clone URLs reach loopback addresses.
	// Link-local and cloud metadata addresses are always blocked.
	AllowPrivateNetworkTargets bool `mapstructure:"allow_private_network_targets"`

	// Password policy applied whenever a password is set (change password, user
	// creation). The minimum length can be raised but never below 8.
	PasswordMinLength        int  `mapstructure:"password_min_length"`
	PasswordRequireUppercase bool `mapstructure:"password_require_uppercase"`
	PasswordRequireLowercase bool `mapstructure:"password_require_lowercase"`
	PasswordRequireNumbers   bool `mapstructure:"password_require_numbers"`
	PasswordRequireSpecial   bool `mapstructure:"password_require_special"`
}

// placeholderSecrets are example values that have shipped in this repository's
// compose files and docs. They are public, so they must never protect data.
var placeholderSecrets = map[string]bool{
	"your-32-byte-long-encryption-key": true,
	"your-32-byte-long-session-secret": true,
	"your-32-byte-long-csrf-secret-12": true,
	"your-32-byte-encryption-key-here": true,
}

// CheckMasterKey returns an error when key is a published placeholder or has
// too little variety to be a random key (e.g. 32 repeated characters). A random
// 32-character hex string essentially never has fewer than 6 distinct symbols.
func CheckMasterKey(key string) error {
	lower := strings.ToLower(strings.TrimSpace(key))
	if placeholderSecrets[lower] || strings.HasPrefix(lower, "your-") ||
		strings.Contains(lower, "changeme") || strings.Contains(lower, "change-me") {
		return errors.New("the key is a published placeholder value")
	}
	distinct := make(map[rune]struct{})
	for _, r := range key {
		distinct[r] = struct{}{}
	}
	if len(distinct) < 6 {
		return errors.New("the key has too little entropy")
	}
	return nil
}

// RateLimitConfig holds rate limiting configuration.
type RateLimitConfig struct {
	AuthRequestsPerMinute int `mapstructure:"auth_requests_per_minute"`
	APIRequestsPerMinute  int `mapstructure:"api_requests_per_minute"`
}

// OAuthConfig holds OAuth2/OIDC provider configuration.
type OAuthConfig struct {
	GitHub OAuthProviderConfig `mapstructure:"github"`
	GitLab OAuthProviderConfig `mapstructure:"gitlab"`
	Google OAuthProviderConfig `mapstructure:"google"`
	// AllowedEmailDomains limits automatic account creation through OAuth to
	// identities whose verified email belongs to one of these domains (e.g.
	// ["example.com"]). Empty allows any domain. Existing accounts (matched by
	// linked identity or email) can always sign in.
	AllowedEmailDomains []string `mapstructure:"allowed_email_domains"`
	// AutoActivate makes accounts created through OAuth sign-up active right
	// away. By default they are created inactive and an admin must enable them
	// before they can sign in.
	AutoActivate bool `mapstructure:"auto_activate"`
}

// OAuthProviderConfig holds a single OAuth provider configuration.
type OAuthProviderConfig struct {
	ClientID     string   `mapstructure:"client_id"`
	ClientSecret string   `mapstructure:"client_secret"`
	RedirectURL  string   `mapstructure:"redirect_url"`
	Scopes       []string `mapstructure:"scopes"`
}

// EmailConfig holds SMTP/email notification configuration.
type EmailConfig struct {
	SMTPHost     string `mapstructure:"smtp_host"`
	SMTPPort     int    `mapstructure:"smtp_port"`
	SMTPUser     string `mapstructure:"smtp_user"`
	SMTPPassword string `mapstructure:"smtp_password"`
	From         string `mapstructure:"from"`
	Enabled      bool   `mapstructure:"enabled"`
	// AllowInsecure permits sending over a plaintext SMTP connection when the
	// server does not offer STARTTLS (e.g. a local relay on localhost:25). By
	// default TLS is required: implicit TLS on port 465, STARTTLS otherwise.
	AllowInsecure bool `mapstructure:"allow_insecure"`
}

// WorkerConfig holds background worker configuration.
type WorkerConfig struct {
	MaxConcurrent    int           `mapstructure:"max_concurrent"`
	CloneTimeout     int           `mapstructure:"clone_timeout"`
	RetryMaxAttempts int           `mapstructure:"retry_max_attempts"`
	RetryBaseDelay   time.Duration `mapstructure:"retry_base_delay"`
}

// LoggingConfig holds logging configuration.
type LoggingConfig struct {
	Level    string `mapstructure:"level"`
	Format   string `mapstructure:"format"`
	Output   string `mapstructure:"output"`
	FilePath string `mapstructure:"file_path"`
	// AuditRetentionDays is how long audit log entries (security records) are
	// kept before the cleanup worker deletes them. 0 keeps them forever.
	AuditRetentionDays int `mapstructure:"audit_retention_days"`
}

// StorageConfig holds storage path configuration.
type StorageConfig struct {
	BasePath      string `mapstructure:"base_path"`
	SSHPath       string `mapstructure:"ssh_path"`
	BackupPath    string `mapstructure:"backup_path"`
	DedupeEnabled bool   `mapstructure:"dedupe_enabled"`
}

// MonitoringConfig holds monitoring/metrics configuration.
type MonitoringConfig struct {
	MetricsEnabled      bool          `mapstructure:"metrics_enabled"`
	MetricsPath         string        `mapstructure:"metrics_path"`
	HealthCheckInterval time.Duration `mapstructure:"health_check_interval"`
}

// Load reads configuration from file and environment variables.
func Load() (*Config, error) {
	return load("")
}

// load implements Load. A non-empty configFile reads that exact file (used by
// tests) instead of searching the standard config paths.
func load(configFile string) (*Config, error) {
	v := viper.New()

	// Set defaults
	v.SetDefault("server.port", 8080)
	v.SetDefault("server.host", "0.0.0.0")
	v.SetDefault("server.read_timeout", "30s")
	v.SetDefault("server.write_timeout", "300s")
	v.SetDefault("server.idle_timeout", "120s")
	v.SetDefault("server.tls.enabled", false)
	v.SetDefault("server.base_url", "http://localhost:8080")

	v.SetDefault("database.host", "localhost")
	v.SetDefault("database.port", 5432)
	v.SetDefault("database.name", "gitmirrors")
	v.SetDefault("database.user", "gitmirrors")
	v.SetDefault("database.password", "")
	v.SetDefault("database.sslmode", "disable")
	v.SetDefault("database.max_open_conns", 25)
	v.SetDefault("database.max_idle_conns", 5)
	v.SetDefault("database.conn_max_lifetime", "1h")

	v.SetDefault("security.session_duration", "24h")
	v.SetDefault("security.rate_limit.auth_requests_per_minute", 10)
	v.SetDefault("security.rate_limit.api_requests_per_minute", 60)
	v.SetDefault("security.hsts_max_age", 31536000) // 1 year in seconds
	v.SetDefault("security.password_min_length", 8)
	v.SetDefault("security.password_require_uppercase", false)
	v.SetDefault("security.password_require_lowercase", false)
	v.SetDefault("security.password_require_numbers", false)
	v.SetDefault("security.password_require_special", false)

	v.SetDefault("email.smtp_port", 587)
	v.SetDefault("email.from", "noreply@gitmirrors.local")
	v.SetDefault("email.enabled", false)
	v.SetDefault("email.allow_insecure", false)

	v.SetDefault("worker.max_concurrent", 3)
	v.SetDefault("worker.clone_timeout", 3600)
	v.SetDefault("worker.retry_max_attempts", 3)
	v.SetDefault("worker.retry_base_delay", "30s")

	v.SetDefault("logging.level", "info")
	v.SetDefault("logging.format", "json")
	v.SetDefault("logging.output", "stdout")
	v.SetDefault("logging.file_path", "/app/logs/app.log")
	v.SetDefault("logging.audit_retention_days", 90)

	v.SetDefault("storage.base_path", "/app/storage/repos")
	v.SetDefault("storage.ssh_path", "/app/storage/ssh")
	v.SetDefault("storage.backup_path", "/app/storage/backups")
	v.SetDefault("storage.dedupe_enabled", true)

	v.SetDefault("monitoring.metrics_enabled", true)
	v.SetDefault("monitoring.metrics_path", "/metrics")
	v.SetDefault("monitoring.health_check_interval", "15m")

	v.SetDefault("oauth.github.scopes", []string{"read:user", "user:email", "repo"})
	v.SetDefault("oauth.gitlab.scopes", []string{"read_api", "read_repository"})
	v.SetDefault("oauth.google.scopes", []string{"email", "profile"})

	// Config file settings
	v.SetConfigType("yaml")
	if configFile != "" {
		v.SetConfigFile(configFile)
	} else {
		v.SetConfigName("config")
		v.AddConfigPath("/app/config")
		v.AddConfigPath("./config")
		v.AddConfigPath(".")
	}

	// Environment variables
	v.AutomaticEnv()
	v.SetEnvPrefix("GITMIRRORS")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))

	// Explicitly bind every config key to its environment variable. viper's
	// AutomaticEnv only resolves env vars during Unmarshal for keys it already
	// knows about (those given a default). Security keys, OAuth credentials and
	// SMTP settings have no defaults, so without this walk their GITMIRRORS_*
	// env vars are silently ignored — which prevents the documented Docker
	// deployment from ever starting (empty master key => encryption init fails).
	bindEnvs(v, Config{})

	// Read config file (ignore if not found)
	if err := v.ReadInConfig(); err != nil {
		var configFileNotFoundError viper.ConfigFileNotFoundError
		if !errors.As(err, &configFileNotFoundError) {
			return nil, fmt.Errorf("error reading config file: %w", err)
		}
	}

	// Map deprecated keys onto their current names, then flag keys this
	// version does not understand (typos, keys from old example files) so they
	// are not silently ignored.
	warnings := migrateLegacyKeys(v)
	warnings = append(warnings, unknownKeyWarnings(v)...)

	var config Config
	if err := v.Unmarshal(&config); err != nil {
		return nil, fmt.Errorf("error unmarshaling config: %w", err)
	}
	config.Warnings = warnings

	// Derive the session-cookie Secure flag from the base_url scheme unless it was
	// set explicitly. No viper default is registered for the key, so IsSet is true
	// only when the operator provided it via config file or GITMIRRORS_SECURITY_
	// COOKIE_SECURE; otherwise an https:// base_url implies Secure cookies.
	if !v.IsSet("security.cookie_secure") {
		config.Security.CookieSecure = strings.HasPrefix(
			strings.ToLower(strings.TrimSpace(config.Server.BaseURL)), "https://")
	}

	if err := config.validate(); err != nil {
		return nil, err
	}

	return &config, nil
}

// legacyKeys maps deprecated (flat or misnamed) keys that older example files
// advertised to their current names.
var legacyKeys = map[string]string{
	"server.tls_enabled":  "server.tls.enabled",
	"server.tls_cert":     "server.tls.cert",
	"server.tls_key":      "server.tls.key",
	"email.smtp_username": "email.smtp_user",
	"email.from_address":  "email.from",
}

// migrateLegacyKeys copies each deprecated key found in the config file onto
// its current name, unless the current name is already set in the file or the
// environment (which then wins). It returns a deprecation warning per key.
func migrateLegacyKeys(v *viper.Viper) []string {
	oldKeys := make([]string, 0, len(legacyKeys))
	for k := range legacyKeys {
		oldKeys = append(oldKeys, k)
	}
	sort.Strings(oldKeys)

	var warnings []string
	for _, oldKey := range oldKeys {
		newKey := legacyKeys[oldKey]
		if !v.InConfig(oldKey) {
			continue
		}
		if _, envSet := os.LookupEnv(envName(newKey)); v.InConfig(newKey) || envSet {
			warnings = append(warnings, fmt.Sprintf(
				"deprecated config key %q is ignored because %q is also set; remove it", oldKey, newKey))
			continue
		}
		v.Set(newKey, v.Get(oldKey))
		warnings = append(warnings, fmt.Sprintf(
			"deprecated config key %q is applied as %q; rename it, support will be removed", oldKey, newKey))
	}
	return warnings
}

// unknownKeyWarnings returns a warning for every key viper knows about (from the
// config file) that maps to no Config field and is not a handled legacy key.
func unknownKeyWarnings(v *viper.Viper) []string {
	known := make(map[string]bool)
	collectKeys(reflect.TypeFor[Config](), "", known)
	var warnings []string
	for _, key := range v.AllKeys() {
		if known[key] || legacyKeys[key] != "" {
			continue
		}
		warnings = append(warnings, fmt.Sprintf("unknown config key %q is not supported and is ignored", key))
	}
	sort.Strings(warnings)
	return warnings
}

// collectKeys records the dotted mapstructure path of every leaf field of t.
func collectKeys(t reflect.Type, prefix string, out map[string]bool) {
	for i := 0; i < t.NumField(); i++ {
		tag := t.Field(i).Tag.Get("mapstructure")
		if tag == "" || tag == "-" {
			continue
		}
		key := tag
		if prefix != "" {
			key = prefix + "." + tag
		}
		if ft := t.Field(i).Type; ft.Kind() == reflect.Struct {
			collectKeys(ft, key, out)
			continue
		}
		out[key] = true
	}
}

// envName returns the GITMIRRORS_* environment variable bound to a config key.
func envName(key string) string {
	return "GITMIRRORS_" + strings.ToUpper(strings.ReplaceAll(key, ".", "_"))
}

// validate rejects settings that would otherwise silently weaken security.
func (c *Config) validate() error {
	var errs []error
	if c.Security.RateLimit.AuthRequestsPerMinute <= 0 {
		errs = append(errs, errors.New("security.rate_limit.auth_requests_per_minute must be positive"))
	}
	if c.Security.RateLimit.APIRequestsPerMinute <= 0 {
		errs = append(errs, errors.New("security.rate_limit.api_requests_per_minute must be positive"))
	}
	if n := c.Security.PasswordMinLength; n < 8 || n > 72 {
		errs = append(errs, fmt.Errorf("security.password_min_length must be between 8 and 72, got %d", n))
	}
	if c.Logging.AuditRetentionDays < 0 {
		errs = append(errs, errors.New("logging.audit_retention_days must not be negative (0 keeps audit logs forever)"))
	}
	if c.Server.TLS.Enabled {
		// Fail at startup rather than when the listener starts (or, worse, run
		// plaintext): TLS was requested, so the key pair must be usable.
		if c.Server.TLS.Cert == "" || c.Server.TLS.Key == "" {
			errs = append(errs, errors.New("server.tls.enabled is true but server.tls.cert and server.tls.key must both be set"))
		} else if _, err := tls.LoadX509KeyPair(c.Server.TLS.Cert, c.Server.TLS.Key); err != nil {
			errs = append(errs, fmt.Errorf("server.tls.enabled is true but the certificate/key cannot be loaded: %w", err))
		}
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("invalid configuration: %w", err)
	}
	return nil
}

// bindEnvs walks a (possibly nested) struct and calls v.BindEnv for every leaf
// field, using the dotted mapstructure path as the key. This makes AutomaticEnv
// work with Unmarshal even for keys that have no default value set.
func bindEnvs(v *viper.Viper, iface any, parts ...string) {
	ift := reflect.TypeOf(iface)
	ifv := reflect.ValueOf(iface)
	for i := 0; i < ift.NumField(); i++ {
		tag := ift.Field(i).Tag.Get("mapstructure")
		if tag == "" || tag == "-" {
			continue
		}
		fieldv := ifv.Field(i)
		if fieldv.Kind() == reflect.Struct {
			bindEnvs(v, fieldv.Interface(), append(parts, tag)...)
			continue
		}
		_ = v.BindEnv(strings.Join(append(parts, tag), "."))
	}
}
