# GitDuppy

GitDuppy is a modern, secure Git repository mirroring and management platform designed for enterprise environments. It provides automated repository synchronization, access control, audit logging, and webhook integrations while maintaining security and compliance standards.

## Features
- Automated Git repository mirroring with configurable schedules
- Role-based access control (RBAC) with fine-grained permissions
- Comprehensive audit logging for all operations
- Webhook notifications for repository events
- Built-in health monitoring and alerting
- Docker-first deployment with Kubernetes support
- End-to-end encryption for sensitive data
- OAuth2 authentication support
- REST API for programmatic management

## Quick Start

The easiest way to get started is using Docker Compose:

```bash
# Clone the repository
git clone https://github.com/yourorg/gitduppy.git
cd gitduppy

# Copy the example environment file
cp .env.example .env

# Start the services
docker-compose up -d

# Access the dashboard at http://localhost:8080
```

## Configuration

GitDuppy can be configured via environment variables or a YAML configuration file. The most important configuration options are:

### Environment Variables
- `GITDUPPY_PORT`: Port to run the server on (default: 8080)
- `DATABASE_URL`: Database connection string (required)
- `ENCRYPTION_KEY`: 32-byte encryption key for sensitive data (required)
- `JWT_SECRET`: Secret for JWT token generation (required)
- `ADMIN_EMAIL`: Initial admin user email (required on first run)

### Configuration File
Create a `config.yaml` file in the root directory or specify its path with `CONFIG_FILE` environment variable.

## API Documentation

Key endpoints:

### Authentication
```bash
# Get JWT token
curl -X POST http://localhost:8080/api/v1/auth/login \
  -H "Content-Type: application/json" \
  -d '{"email": "admin@example.com", "password": "yourpassword"}'
```

### Repository Management
```bash
# List repositories
curl -X GET http://localhost:8080/api/v1/repositories \
  -H "Authorization: Bearer <your-token>"
```

## Security Considerations

- Always use HTTPS in production
- Store encryption keys securely (never in version control)
- Regularly rotate JWT secrets and encryption keys
- Enable audit logging for compliance
- Use strong passwords and enable 2FA for admin accounts

## Production Deployment

For production, use the provided `docker-compose.prod.yml`:

```bash
docker-compose -f docker-compose.prod.yml up -d
```

This includes Caddy as a reverse proxy with automatic HTTPS.

## Contributing

Contributions are welcome! Please follow these steps:
1. Fork the repository
2. Create a feature branch
3. Write tests for your changes
4. Submit a pull request

## License

GitDuppy is licensed under the MIT License. See [LICENSE](LICENSE) for details.

---

For detailed documentation, see the [docs/](docs/) directory.