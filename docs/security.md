# Security Guide

GitDuppy is designed with security as a top priority. This guide covers security best practices, hardening techniques, and compliance considerations.

## Encryption Key Management

GitDuppy encrypts sensitive data (SSH keys, API tokens) in the database using AES-256-GCM encryption. Proper key management is critical for security.

### Best Practices for Encryption Keys
- **Never store encryption keys in version control**
- Use environment variables or secure key management systems to provide keys at runtime
- Rotate encryption keys periodically (at least annually)
- When rotating keys, GitDuppy will automatically re-encrypt existing data on next access
- Store backup copies of encryption keys in a secure location (hardware security module, encrypted vault)

### Key Rotation Procedure
1. Generate a new 32-byte encryption key
2. Update the `ENCRYPTION_KEY` environment variable or configuration file
3. Restart GitDuppy services
4. Existing encrypted data will be decrypted with the old key and re-encrypted with the new key on next access
5. Monitor logs for any decryption errors during the transition period

## TLS Configuration

Always use HTTPS in production environments to protect data in transit.

### Enabling TLS in GitDuppy
```yaml
# config.yaml
server:
  tls_enabled: true
  tls_cert: "/path/to/cert.pem"
  tls_key: "/path/to/key.pem"
```

### Using Let's Encrypt with Caddy
The production Docker Compose file includes Caddy, which automatically obtains and renews Let's Encrypt certificates:

```caddyfile
# Caddyfile
gitduppy.example.com {
    reverse_proxy gitduppy:8080
    tls admin@example.com
}
```

### TLS Best Practices
- Use strong cipher suites (disable weak ciphers like RC4, DES)
- Enable HTTP Strict Transport Security (HSTS)
- Use certificate pinning if required by your security policy
- Regularly update TLS certificates before expiration
- Monitor for certificate revocation

## Authentication and Authorization

### Password Security
GitDuppy enforces strong password policies by default:
- Minimum 8 characters
- Requires uppercase, lowercase, numbers, and special characters
- Passwords are hashed using bcrypt with cost factor 12

Configure password requirements in the configuration file:
```yaml
security:
  password_min_length: 12
  password_require_uppercase: true
  password_require_lowercase: true
  password_require_numbers: true
  password_require_special: true
```

### JWT Token Security
- Use long, random JWT secrets (minimum 32 characters)
- Set appropriate token expiration times (default: 24 hours)
- Implement token refresh mechanisms for long-lived sessions
- Store JWT tokens securely (HttpOnly cookies recommended for web applications)

### Role-Based Access Control (RBAC)
GitDuppy supports two roles:
- **Admin**: Full access to all features
- **User**: Can manage their own repositories and view clone logs

Assign roles carefully and follow the principle of least privilege.

## Audit Logging

GitDuppy maintains comprehensive audit logs of all operations for compliance and security monitoring.

### Audit Log Configuration
```yaml
logging:
  audit_enabled: true
  audit_retention_days: 90
```

### Audit Log Contents
Audit logs include:
- Timestamp of the operation
- User who performed the operation
- IP address of the client
- Operation type (create, update, delete, login, etc.)
- Resource affected (repository ID, user ID, etc.)
- Success/failure status

### Audit Log Best Practices
- Enable audit logging in production environments
- Retain logs for at least 90 days (or as required by compliance regulations)
- Forward logs to a secure log management system
- Set up alerts for suspicious activities (multiple failed logins, unauthorized access attempts)
- Regularly review audit logs for anomalies

## Network Security

### Firewall Configuration
Restrict access to GitDuppy services:
- Only allow HTTPS (port 443) from external networks
- Restrict database access to GitDuppy application servers only
- Use network segmentation to isolate GitDuppy components

### Rate Limiting
GitDuppy includes built-in rate limiting to prevent brute force attacks:

```yaml
middleware:
  rate_limit:
    enabled: true
    requests_per_minute: 60
    burst: 10
```

Adjust rate limits based on your usage patterns and security requirements.

## Container Security

When deploying with Docker:
- Run containers as non-root users
- Use minimal base images (Alpine Linux recommended)
- Regularly update container images to patch vulnerabilities
- Scan container images for vulnerabilities before deployment
- Limit container capabilities and resources

Example secure Docker Compose configuration:
```yaml
version: '3.8'
services:
  gitduppy:
    image: gitduppy/gitduppy:latest
    user: "1000:1000" # Run as non-root user
    read_only: true # Make filesystem read-only
    tmpfs: /tmp # Use tmpfs for temporary files
    security_opt:
      - no-new-privileges:true
    cap_drop:
      - ALL
    cap_add:
      - NET_BIND_SERVICE
```

## Backup and Disaster Recovery

### Database Backups
Regularly backup your PostgreSQL database:
```bash
# Create backup
pg_dump -h localhost -U gitduppy -W gitduppy > gitduppy-backup-$(date +%Y%m%d).sql

# Restore backup
psql -h localhost -U gitduppy -W gitduppy < gitduppy-backup.sql
```

### Configuration Backups
Backup your configuration files and environment variables:
- Store configuration files in version control (excluding secrets)
- Keep encrypted backups of environment files containing secrets
- Document your deployment configuration for disaster recovery

## Compliance Considerations

GitDuppy can help meet various compliance requirements:

### GDPR Compliance
- Audit logs provide record of data processing activities
- Data encryption protects personal data at rest
- Role-based access control limits data access to authorized personnel
- Data retention policies can be configured for audit logs

### HIPAA Compliance
- Encryption of sensitive data meets HIPAA requirements
- Audit logging provides accountability for data access
- Access controls prevent unauthorized access to protected health information

### PCI DSS Compliance
- TLS encryption for data in transit
- Strong authentication mechanisms
- Regular security updates and vulnerability scanning
- Segregation of duties through RBAC

## Security Monitoring

Set up security monitoring for production deployments:

### Intrusion Detection
- Monitor for multiple failed login attempts
- Alert on unauthorized access attempts
- Monitor for unusual patterns of repository access

### Vulnerability Scanning
- Regularly scan container images for vulnerabilities
- Monitor dependencies for security updates
- Subscribe to security advisories for GitDuppy and its dependencies

### Security Headers
Ensure your reverse proxy adds security headers:
- Content-Security-Policy
- X-Content-Type-Options: nosniff
- X-Frame-Options: DENY
- X-XSS-Protection: 1; mode=block
- Referrer-Policy: strict-origin-when-cross-origin