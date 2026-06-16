# Migration from GitMirrors

This guide provides instructions for migrating from the original GitMirrors project to GitDuppy. The migration process includes data migration, configuration updates, and deployment changes.

## Overview

GitDuppy is a complete rewrite of GitMirrors with enhanced security, better performance, and additional features. While the core functionality remains similar, there are significant changes in:

- Database schema
- Configuration format
- API endpoints
- Authentication system
- Deployment architecture

## Prerequisites

Before starting the migration:

1. **Backup your GitMirrors installation**
   - Database backup
   - Configuration files backup
   - Repository data backup

2. **Review breaking changes**
   - GitDuppy requires PostgreSQL 12+ (GitMirrors may have used older versions)
   - New authentication system with JWT tokens
   - Enhanced security requirements (encryption keys, stronger passwords)

3. **Test migration in staging environment first**

## Data Migration

### Using the Migration Tool

GitDuppy includes a built-in migration tool to transfer data from GitMirrors:

```bash
# Run migration tool
docker-compose run --rm gitduppy migrate-from-gitmirrors \
  --source-db-url="postgres://olduser:oldpass@oldhost:5432/gitmirrors" \
  --target-db-url="postgres://newuser:newpass@localhost:5432/gitduppy" \
  --encryption-key="your-32-byte-encryption-key"
```

### Manual Data Migration

If the automated tool doesn't work for your setup, you can migrate data manually:

1. **Export GitMirrors data**
   ```sql
   -- Export repositories
   COPY (SELECT id, name, source_url, destination_path, schedule, created_at, updated_at FROM repositories) TO '/tmp/repositories.csv' WITH CSV HEADER;
   
   -- Export users
   COPY (SELECT id, email, password_hash, role, created_at, updated_at FROM users) TO '/tmp/users.csv' WITH CSV HEADER;
   ```

2. **Transform data for GitDuppy schema**
   - Update column names to match GitDuppy schema
   - Hash passwords using bcrypt (GitDuppy uses bcrypt instead of whatever GitMirrors used)
   - Add required fields with default values

3. **Import into GitDuppy database**
   ```sql
   -- Import repositories
   COPY repositories(id, name, source_url, destination_path, schedule, created_at, updated_at) FROM '/tmp/repositories_transformed.csv' WITH CSV HEADER;
   
   -- Import users
   COPY users(id, email, password_hash, role, created_at, updated_at) FROM '/tmp/users_transformed.csv' WITH CSV HEADER;
   ```

## Configuration Migration

### Environment Variables

Map old GitMirrors environment variables to new GitDuppy variables:

| GitMirrors Variable | GitDuppy Variable | Notes |
|---------------------|-------------------|-------|
| `PORT` | `GITDUPPY_SERVER_PORT` | Same functionality |
| `DATABASE_URL` | `GITDUPPY_DATABASE_URL` | Same format |
| `JWT_SECRET` | `GITDUPPY_SECURITY_JWT_SECRET` | Same purpose |
| `ADMIN_EMAIL` | `GITDUPPY_SECURITY_ADMIN_EMAIL` | Same purpose |
| N/A | `GITDUPPY_SECURITY_ENCRYPTION_KEY` | **New required variable** |

### Configuration File

Convert your GitMirrors configuration file to GitDuppy format:

**GitMirrors config.json:**
```json
{
  "server": {
    "port": 8080,
    "host": "0.0.0.0"
  },
  "database": {
    "url": "postgres://user:pass@localhost:5432/gitmirrors"
  },
  "jwt_secret": "your-secret-here",
  "admin_email": "admin@example.com"
}
```

**GitDuppy config.yaml:**
```yaml
server:
  port: 8080
  host: "0.0.0.0"
  tls_enabled: false

database:
  url: "postgres://user:pass@localhost:5432/gitduppy"
  max_connections: 25
  max_idle_connections: 5
  connection_max_lifetime: 300

security:
  jwt_secret: "your-secret-here"
  encryption_key: "your-32-byte-encryption-key-here" # Required!
  admin_email: "admin@example.com"
  password_min_length: 8
  password_require_uppercase: true
  password_require_lowercase: true
  password_require_numbers: true
  password_require_special: true

git:
  clone_timeout: 300
  fetch_timeout: 120
  max_concurrent_clones: 5
  retry_attempts: 3
  retry_delay: 30

webhooks:
  enabled: true
  timeout: 30
  max_retries: 3
  retry_delay: 60

logging:
  level: "info"
  format: "json"
  audit_enabled: true
  audit_retention_days: 90

email:
  enabled: false
  smtp_host: "smtp.example.com"
  smtp_port: 587
  smtp_username: ""
  smtp_password: ""
  from_address: "noreply@example.com"
```

## API Migration

Update your API clients to use GitDuppy endpoints:

### Authentication
**GitMirrors:**
```bash
curl -X POST http://localhost:8080/login \
  -H "Content-Type: application/json" \
  -d '{"email": "user@example.com", "password": "password"}'
```

**GitDuppy:**
```bash
curl -X POST http://localhost:8080/api/v1/auth/login \
  -H "Content-Type: application/json" \
  -d '{"email": "user@example.com", "password": "password"}'
```

### Repository Management
**GitMirrors:**
```bash
curl -X GET http://localhost:8080/repositories \
  -H "Authorization: Bearer token"
```

**GitDuppy:**
```bash
curl -X GET http://localhost:8080/api/v1/repositories \
  -H "Authorization: Bearer token"
```

### Key Changes:
- All endpoints moved under `/api/v1/` prefix
- Authentication endpoint changed from `/login` to `/api/v1/auth/login`
- Logout endpoint added at `/api/v1/auth/logout`
- New endpoints for clone jobs, health checks, and user management

## Deployment Migration

### Docker Compose

Update your `docker-compose.yml` file:

**GitMirrors docker-compose.yml:**
```yaml
version: '3'
services:
  gitmirrors:
    image: gitmirrors/gitmirrors:latest
    ports:
      - "8080:8080"
    environment:
      - DATABASE_URL=postgres://user:pass@db:5432/gitmirrors
      - JWT_SECRET=your-secret
    depends_on:
      - db
  
  db:
    image: postgres:11
    environment:
      - POSTGRES_DB=gitmirrors
      - POSTGRES_USER=user
      - POSTGRES_PASSWORD=pass
```

**GitDuppy docker-compose.yml:**
```yaml
version: '3.8'
services:
  gitduppy:
    image: gitduppy/gitduppy:latest
    ports:
      - "8080:8080"
    environment:
      - GITDUPPY_DATABASE_URL=postgres://user:pass@db:5432/gitduppy
      - GITDUPPY_SECURITY_JWT_SECRET=your-secret
      - GITDUPPY_SECURITY_ENCRYPTION_KEY=your-32-byte-key
      - GITDUPPY_SECURITY_ADMIN_EMAIL=admin@example.com
    depends_on:
      - db
    volumes:
      - ./data:/data
  
  db:
    image: postgres:14
    environment:
      - POSTGRES_DB=gitduppy
      - POSTGRES_USER=user
      - POSTGRES_PASSWORD=pass
    volumes:
      - db-data:/var/lib/postgresql/data

volumes:
  db-data:
```

### Kubernetes Migration

Update your Kubernetes manifests to use GitDuppy images and configuration:

1. Change image from `gitmirrors/gitmirrors` to `gitduppy/gitduppy`
2. Update environment variable names
3. Add the new required `ENCRYPTION_KEY` environment variable
4. Update database version from PostgreSQL 11 to 14+
5. Add volume mounts for data persistence

## Post-Migration Steps

1. **Verify data integrity**
   - Check that all repositories migrated correctly
   - Verify user accounts and permissions
   - Test repository cloning functionality

2. **Update monitoring and alerting**
   - Update health check endpoints
   - Configure new metrics endpoints
   - Update log parsing rules for new log format

3. **Update documentation and runbooks**
   - Replace GitMirrors references with GitDuppy
   - Update configuration examples
   - Document new features and capabilities

4. **Train users on new features**
   - Webhook notifications
   - Enhanced audit logging
   - Role-based access control improvements
   - New API endpoints

## Common Migration Issues

### Encryption Key Errors
**Issue:** "Invalid encryption key" errors after migration.

**Solution:** Ensure you're using a 32-byte encryption key. Generate one using:
```bash
openssl rand -base64 32
```

### Database Schema Mismatch
**Issue:** Migration fails due to schema differences.

**Solution:** Run database migrations before attempting data migration:
```bash
docker-compose run --rm gitduppy migrate up
```

### Authentication Failures
**Issue:** Users can't log in after migration.

**Solution:** Password hashing algorithm changed. Users will need to reset their passwords, or you'll need to re-hash passwords using bcrypt.

### Repository Path Issues
**Issue:** Cloned repositories have incorrect paths.

**Solution:** Update repository destination paths in the database to match your new deployment structure.

## Rollback Plan

If migration fails, you can rollback to GitMirrors:

1. Stop GitDuppy services
2. Restore GitMirrors database from backup
3. Restore GitMirrors configuration files
4. Start GitMirrors services
5. Verify functionality

Keep your GitMirrors backup available for at least 30 days after successful migration.