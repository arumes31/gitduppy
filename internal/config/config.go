package config

import (
	"errors"
	"fmt"
	"reflect"
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
}

// StorageConfig holds storage path configuration.
type StorageConfig struct {
	BasePath   string `mapstructure:"base_path"`
	SSHPath    string `mapstructure:"ssh_path"`
	BackupPath string `mapstructure:"backup_path"`
}

// MonitoringConfig holds monitoring/metrics configuration.
type MonitoringConfig struct {
	MetricsEnabled      bool          `mapstructure:"metrics_enabled"`
	MetricsPath         string        `mapstructure:"metrics_path"`
	HealthCheckInterval time.Duration `mapstructure:"health_check_interval"`
}

// Load reads configuration from file and environment variables.
func Load() (*Config, error) {
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

	v.SetDefault("email.smtp_port", 587)
	v.SetDefault("email.from", "noreply@gitmirrors.local")
	v.SetDefault("email.enabled", false)

	v.SetDefault("worker.max_concurrent", 3)
	v.SetDefault("worker.clone_timeout", 3600)
	v.SetDefault("worker.retry_max_attempts", 3)
	v.SetDefault("worker.retry_base_delay", "30s")

	v.SetDefault("logging.level", "info")
	v.SetDefault("logging.format", "json")
	v.SetDefault("logging.output", "stdout")
	v.SetDefault("logging.file_path", "/app/logs/app.log")

	v.SetDefault("storage.base_path", "/app/storage/repos")
	v.SetDefault("storage.ssh_path", "/app/storage/ssh")
	v.SetDefault("storage.backup_path", "/app/storage/backups")

	v.SetDefault("monitoring.metrics_enabled", true)
	v.SetDefault("monitoring.metrics_path", "/metrics")
	v.SetDefault("monitoring.health_check_interval", "15m")

	// Config file settings
	v.SetConfigName("config")
	v.SetConfigType("yaml")
	v.AddConfigPath("/app/config")
	v.AddConfigPath("./config")
	v.AddConfigPath(".")

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

	var config Config
	if err := v.Unmarshal(&config); err != nil {
		return nil, fmt.Errorf("error unmarshaling config: %w", err)
	}

	return &config, nil
}

// bindEnvs walks a (possibly nested) struct and calls v.BindEnv for every leaf
// field, using the dotted mapstructure path as the key. This makes AutomaticEnv
// work with Unmarshal even for keys that have no default value set.
func bindEnvs(v *viper.Viper, iface interface{}, parts ...string) {
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
