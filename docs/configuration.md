# Configuration Reference

GitDuppy supports configuration through environment variables, a YAML configuration file, or a combination of both. Environment variables take precedence over configuration file values.

## Configuration File

The default configuration file is `config.yaml` in the root directory. You can specify a different path using the `CONFIG_FILE` environment variable.

### Example Configuration File
```yaml
# Server configuration
server:
  port: 8080
  host: "0.0.0.0"
  tls_enabled: false
  tls_cert: ""
  tls_key: ""

# Database configuration
database:
  url: "postgres://user:password@localhost:5432/gitduppy?sslmode=disable"
  max_connections: 25
  max_idle_connections: 5
  connection_max_lifetime: 300

# Security configuration
security:
  jwt_secret: "your-jwt-secret-here" # Required
  encryption_key: "your-32-byte-encryption-key" # Required
  admin_email: "admin@example.com" # Required on first run
  password_min_length: 8
  password_require_uppercase: true
  password_require_lowercase: true
  password_require_numbers: true
  password_require_special: true

# Git operations configuration
git:
  clone_timeout: 300 # seconds
  fetch_timeout: 120 # seconds
  max_concurrent_clones: 5
  retry_attempts: 3
  retry_delay: 30 # seconds

# Webhook configuration
webhooks:
  enabled: true
  timeout: 30 # seconds
  max_retries: 3
  retry_delay: 60 # seconds

# Logging configuration
logging:
  level: "info"
  format: "json"
  audit_enabled: true
  audit_retention_days: 90

# Email configuration (for notifications)
email:
  enabled: false
  smtp_host: "smtp.example.com"
  smtp_port: 587
  smtp_username: ""
  smtp_password: ""
  from_address: "noreply@example.com"
```

## Environment Variables

All configuration options can be set via environment variables. The environment variable names follow this pattern: `GITDUPPY_<SECTION>_<KEY>` with dots replaced by underscores and all uppercase.

### Required Environment Variables
- `GITDUPPY_SECURITY_JWT_SECRET`: Secret used for JWT token generation (minimum 32 characters recommended)
- `GITDUPPY_SECURITY_ENCRYPTION_KEY`: 32-byte key for encrypting sensitive data in the database
- `GITDUPPY_DATABASE_URL`: Database connection string (PostgreSQL format)
- `GITDUPPY_SECURITY_ADMIN_EMAIL`: Email address for the initial admin user (required on first run)

### Optional Environment Variables
- `GITDUPPY_SERVER_PORT`: Port to run the server on (default: 8080)
- `GITDUPPY_SERVER_HOST`: Host to bind to (default: "0.0.0.0")
- `GITDUPPY_LOGGING_LEVEL`: Log level (debug, info, warn, error) (default: "info")
- `GITDUPPY_GIT_MAX_CONCURRENT_CLONES`: Maximum number of concurrent git clone operations (default: 5)
- `GITDUPPY_WEBHOOKS_ENABLED`: Enable webhook notifications (default: true)
- `GITDUPPY_EMAIL_ENABLED`: Enable email notifications (default: false)

## Configuration Precedence

Configuration values are loaded in this order (later values override earlier ones):
1. Default values hardcoded in the application
2. Values from the configuration file (if specified)
3. Environment variables

## Best Practices

- Never commit sensitive configuration (JWT secret, encryption key) to version control
- Use environment variables for secrets in production environments
- Keep configuration files in version control for non-sensitive settings
- Use different configuration files for different environments (development, staging, production)
- Regularly rotate JWT secrets and encryption keys
- Store encryption keys in a secure key management system when possible