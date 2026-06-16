# GitDuppy

<p align="center"><img src="static/images/logo.png" width="300" alt="GitDuppy Logo"></p>

[![Go Version](https://img.shields.io/badge/go-1.26.4-blue)](https://go.dev)
[![CI/CD](https://github.com/gitduppy/gitduppy/actions/workflows/ci.yml/badge.svg)](https://github.com/gitduppy/gitduppy/actions)
[![Code Quality](https://img.shields.io/badge/golangci--lint-passing-brightgreen)](https://golangci-lint.run/)
[![Security Scans](https://img.shields.io/badge/gosec-passing-brightgreen)](https://github.com/securego/gosec)
[![Secret Scanning](https://img.shields.io/badge/gitleaks-passing-brightgreen)](https://gitleaks.io/)
[![License](https://img.shields.io/badge/license-MIT-blue)](LICENSE)

A modern, secure Git repository mirroring and management platform designed for enterprise environments. GitDuppy provides automated repository synchronization, access control, audit logging, and webhook integrations while maintaining security and compliance standards.

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

# Copy the example configuration file
cp config.example.yaml config.yaml

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
Reference the provided `config.example.yaml` file for complete configuration options. Create a `config.yaml` file in the root directory or specify its path with `CONFIG_FILE` environment variable.

## Usage

Build and run the server binary:

```bash
go build -o gitduppy cmd/server/main.go
./gitduppy
```

## API Reference

See the full API documentation in [docs/api-reference.md](docs/api-reference.md).

## Deployment

For production deployments, use the provided Docker Compose files:

- **Development**: `docker-compose.yml`
- **Production**: `docker-compose.prod.yml` with Caddy reverse proxy

The Caddy configuration is located at [deployments/caddy/Caddyfile](deployments/caddy/Caddyfile).

## Security

For comprehensive security guidelines, see [docs/security.md](docs/security.md).

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