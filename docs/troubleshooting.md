# Troubleshooting Guide

This guide covers common issues with GitDuppy and their solutions.

## Docker Issues

### Container fails to start
**Symptoms:** Container exits immediately or crashes on startup.

**Solutions:**
1. Check logs for specific error messages:
   ```bash
   docker-compose logs gitduppy
   ```
2. Verify all required environment variables are set in your `.env` file:
   - `DATABASE_URL`
   - `ENCRYPTION_KEY`
   - `JWT_SECRET`
   - `ADMIN_EMAIL`
3. Ensure the database container is running and accessible:
   ```bash
   docker-compose ps
   docker-compose logs db
   ```
4. Check if ports are already in use:
   ```bash
   netstat -tlnp | grep :8080
   ```

### Database connection errors
**Symptoms:** "Connection refused" or "database does not exist" errors.

**Solutions:**
1. Verify database URL format:
   ```bash
   # Correct format
   postgres://username:password@host:port/database?sslmode=disable
   ```
2. Check if the database is initialized:
   ```bash
   # Connect to database container
   docker-compose exec db psql -U gitduppy -d gitduppy
   
   # Check if tables exist
   \dt
   ```
3. If tables don't exist, initialize the database:
   ```bash
   # Run database migrations
   docker-compose run --rm gitduppy migrate up
   ```
4. For SSL connection issues, try adding `?sslmode=disable` to the database URL (development only).

### Permission denied on volume mounts
**Symptoms:** "Permission denied" errors when accessing mounted volumes.

**Solutions:**
1. Check directory permissions:
   ```bash
   ls -la ./data
   ```
2. Set appropriate ownership:
   ```bash
   sudo chown -R 1000:1000 ./data
   ```
3. Or run container with specific user ID:
   ```yaml
   # docker-compose.yml
   services:
     gitduppy:
       user: "${UID:-1000}:${GID:-1000}"
   ```

## Database Issues

### Migration failures
**Symptoms:** Database migration errors during startup.

**Solutions:**
1. Check current migration status:
   ```bash
   docker-compose run --rm gitduppy migrate status
   ```
2. If migration is stuck, try rolling back:
   ```bash
   docker-compose run --rm gitduppy migrate down 1
   docker-compose run --rm gitduppy migrate up
   ```
3. For production databases, always backup before running migrations:
   ```bash
   pg_dump -h localhost -U gitduppy -W gitduppy > backup.sql
   ```

### Performance issues
**Symptoms:** Slow response times, timeouts during operations.

**Solutions:**
1. Check database connection pool settings:
   ```yaml
   database:
     max_connections: 25
     max_idle_connections: 5
   ```
2. Monitor database performance:
   ```sql
   -- Check active connections
   SELECT count(*) FROM pg_stat_activity WHERE datname = 'gitduppy';
   
   -- Check slow queries
   SELECT query, total_time FROM pg_stat_statements ORDER BY total_time DESC LIMIT 5;
   ```
3. Add database indexes for frequently queried columns:
   ```sql
   CREATE INDEX idx_repositories_source_url ON repositories(source_url);
   CREATE INDEX idx_clone_jobs_repository_id ON clone_jobs(repository_id);
   ```

## Git Operations Issues

### Clone failures
**Symptoms:** Repository clones fail with authentication or network errors.

**Solutions:**
1. Verify SSH key format and permissions:
   ```bash
   # Test SSH key
   ssh -T git@github.com -i /path/to/ssh_key
   ```
2. Check if SSH key is properly formatted in configuration:
   ```yaml
   ssh_key: |
     -----BEGIN RSA PRIVATE KEY-----
     MIIEowIBAAKCAQEA...
     -----END RSA PRIVATE KEY-----
   ```
3. Increase clone timeout for large repositories:
   ```yaml
   git:
     clone_timeout: 600  # 10 minutes
   ```
4. Check network connectivity from container:
   ```bash
   docker-compose exec gitduppy ping github.com
   docker-compose exec gitduppy curl -I https://github.com
   ```

### Authentication failures
**Symptoms:** "Authentication failed" errors when cloning private repositories.

**Solutions:**
1. Verify SSH key has proper access to repository
2. Test SSH key manually:
   ```bash
   docker-compose exec gitduppy ssh -T git@github.com -i /path/to/key
   ```
3. For HTTPS repositories, ensure username/password or token is correct
4. Check if repository URL is correct (HTTPS vs SSH)

## API Issues

### Authentication errors
**Symptoms:** 401 Unauthorized responses from API endpoints.

**Solutions:**
1. Verify JWT token is included in Authorization header:
   ```bash
   curl -H "Authorization: Bearer your-token-here" http://localhost:8080/api/v1/repositories
   ```
2. Check if token has expired (default 24 hours)
3. Verify token signature matches server's JWT secret
4. Check user account status (not disabled or deleted)

### CORS errors
**Symptoms:** Browser blocks API requests due to CORS policy.

**Solutions:**
1. Configure allowed origins in config.yaml:
   ```yaml
   middleware:
     cors:
       allowed_origins:
         - "https://your-frontend.example.com"
         - "http://localhost:3000"
   ```
2. For development, allow all origins (not recommended for production):
   ```yaml
   middleware:
     cors:
       allowed_origins: ["*"]
   ```
3. Ensure frontend sends proper headers:
   ```javascript
   fetch('/api/v1/repositories', {
     headers: {
       'Authorization': 'Bearer ' + token,
       'Content-Type': 'application/json'
     }
   })
   ```

## Configuration Issues

### Environment variables not taking effect
**Symptoms:** Configuration changes via environment variables have no effect.

**Solutions:**
1. Verify environment variable names match expected format:
   ```bash
   # Correct format
   GITDUPPY_SERVER_PORT=9090
   GITDUPPY_SECURITY_JWT_SECRET=your-secret-here
   ```
2. Check if variables are loaded in container:
   ```bash
   docker-compose exec gitduppy env | grep GITDUPPY
   ```
3. Restart containers after changing environment variables:
   ```bash
   docker-compose down
   docker-compose up -d
   ```
4. Verify variable precedence (environment variables override config file)

### Configuration file not found
**Symptoms:** "Config file not found" errors.

**Solutions:**
1. Verify config file path:
   ```bash
   # Check if file exists
   ls -la ./config.yaml
   ```
2. Mount config file correctly in Docker:
   ```yaml
   # docker-compose.yml
   services:
     gitduppy:
       volumes:
         - ./config.yaml:/app/config.yaml
       environment:
         - CONFIG_FILE=/app/config.yaml
   ```
3. Set correct file permissions:
   ```bash
   chmod 644 config.yaml
   ```

## Logging and Monitoring Issues

### Missing log entries
**Symptoms:** Expected log entries don't appear in logs.

**Solutions:**
1. Check log level configuration:
   ```yaml
   logging:
     level: "debug"  # Change from "info" for more verbose logging
   ```
2. Verify log output destination:
   ```bash
   # Check container logs
   docker-compose logs gitduppy
   
   # Check if logs are being written to file
   docker-compose exec gitduppy ls -la /var/log/gitduppy/
   ```
3. Restart service after changing log configuration

### Audit logs not recording
**Symptoms:** Audit events are not being recorded.

**Solutions:**
1. Verify audit logging is enabled:
   ```yaml
   logging:
     audit_enabled: true
   ```
2. Check audit log table in database:
   ```sql
   SELECT COUNT(*) FROM audit_logs;
   SELECT * FROM audit_logs ORDER BY created_at DESC LIMIT 5;
   ```
3. Verify database user has INSERT permissions on audit_logs table

## Migration from GitMirrors

### Data migration failures
**Symptoms:** Errors when migrating data from original GitMirrors.

**Solutions:**
1. Verify source database connection:
   ```bash
   # Test connection to old GitMirrors database
   psql -h old-host -U old-user -d old-database -c "SELECT 1"
   ```
2. Run migration tool with verbose output:
   ```bash
   docker-compose run --rm gitduppy migrate-from-gitmirrors --source-db-url="old-connection-string" --verbose
   ```
3. Check for schema compatibility issues
4. Backup both databases before migration

### Configuration migration issues
**Symptoms:** Settings don't work after migration from GitMirrors.

**Solutions:**
1. Map old configuration options to new ones using the migration guide
2. Verify all required new configuration options are set
3. Test migrated configuration in staging environment first
4. Update any scripts or automation that reference old configuration format

## General Debugging Tips

1. **Enable debug logging** for detailed information:
   ```yaml
   logging:
     level: "debug"
   ```

2. **Check system resources**:
   ```bash
   docker stats
   free -h
   df -h
   ```

3. **Test components individually**:
   ```bash
   # Test database connection
   docker-compose exec gitduppy curl -f http://db:5432
   
   # Test external connectivity
   docker-compose exec gitduppy curl -I https://api.github.com
   ```

4. **Use the health check endpoint**:
   ```bash
   curl http://localhost:8080/api/v1/health