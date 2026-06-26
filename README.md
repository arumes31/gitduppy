# GitDuppy

<p align="center"><img src="static/images/logo.png" width="300" alt="GitDuppy Logo"></p>

[![Go Version](https://img.shields.io/badge/go-1.26.4-blue)](https://go.dev)
[![CI/CD](https://github.com/gitduppy/gitduppy/actions/workflows/build.yml/badge.svg)](https://github.com/gitduppy/gitduppy/actions)
[![Code Quality](https://img.shields.io/badge/golangci--lint-passing-brightgreen)](https://golangci-lint.run/)
[![Security Scans](https://img.shields.io/badge/gosec-passing-brightgreen)](https://github.com/securego/gosec)
[![Secret Scanning](https://img.shields.io/badge/gitleaks-passing-brightgreen)](https://gitleaks.io/)
[![License](https://img.shields.io/badge/license-MIT-blue)](LICENSE)

A modern, secure Git repository mirroring and management platform designed for enterprise environments. GitDuppy provides automated repository synchronization, access control, audit logging, and webhook integrations while maintaining security and compliance standards.

## Features

- Automated Git repository mirroring with configurable schedules
- Full GitHub Metadata Mirroring (Issues, PRs, Releases, and Wikis)
- GitHub-like web repository browser — browse files, commits, diffs in-browser
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
docker compose up -d --build

# Access the dashboard at http://localhost:7659/login (or http://localhost:8080 inside the container)
```

### Default Login Credentials

On the first run, GitDuppy automatically seeds a default administrator account. You can log in with:
- **Email**: `admin@gitmirrors.local`
- **Password**: `admin123`

> [!IMPORTANT]
> You must change this default password immediately upon your first successful login for security compliance.

---

## Configuration

GitDuppy can be configured via a YAML configuration file (`config.yaml`) or by environment variables. 

All environment variables are prefixed with `GITMIRRORS_` and map to the YAML structure using underscores instead of dots (e.g., `security.master_key` maps to `GITMIRRORS_SECURITY_MASTER_KEY`).

### Core Authentication & Security Configurations

To secure your installation, you must generate three 32-byte keys for credential encryption, session signing, and CSRF protection:

1. **Master Key** (`GITMIRRORS_SECURITY_MASTER_KEY` / `security.master_key`): A 32-byte hex-encoded AES key used to encrypt repository credentials in the database.
2. **Session Secret** (`GITMIRRORS_SECURITY_SESSION_SECRET` / `security.session_secret`): A 32-byte hex-encoded secret used to sign session cookies.
3. **CSRF Key** (`GITMIRRORS_SECURITY_CSRF_KEY` / `security.csrf_key`): A 32-byte hex-encoded key used for CSRF token generation.

#### Generating Keys
You can generate secure 32-byte hex keys using the following commands:
- **Bash**: `openssl rand -hex 32`
- **PowerShell**: `[Convert]::ToHexString((1..32 | % { Get-Random -Min 0 -Max 256 } -as [byte[]]))`

### OAuth2 Configurations

GitDuppy supports GitHub, GitLab, and Google OAuth2 for user authentication. These can be configured in your `config.yaml` or via env variables:

- **GitHub OAuth**:
  - `GITMIRRORS_OAUTH_GITHUB_CLIENT_ID` / `oauth.github.client_id`
  - `GITMIRRORS_OAUTH_GITHUB_CLIENT_SECRET` / `oauth.github.client_secret`
  - `GITMIRRORS_OAUTH_GITHUB_REDIRECT_URL` (e.g., `http://localhost:7659/api/v1/auth/oauth/github/callback`)
- **GitLab OAuth**:
  - `GITMIRRORS_OAUTH_GITLAB_CLIENT_ID` / `oauth.gitlab.client_id`
  - `GITMIRRORS_OAUTH_GITLAB_CLIENT_SECRET` / `oauth.gitlab.client_secret`
  - `GITMIRRORS_OAUTH_GITLAB_REDIRECT_URL`
- **Google OAuth**:
  - `GITMIRRORS_OAUTH_GOOGLE_CLIENT_ID` / `oauth.google.client_id`
  - `GITMIRRORS_OAUTH_GOOGLE_CLIENT_SECRET` / `oauth.google.client_secret`
  - `GITMIRRORS_OAUTH_GOOGLE_REDIRECT_URL`

### Configuration File
Reference the provided `config.example.yaml` file for complete configuration options. Create a `config.yaml` file in the root directory or specify its path with the `CONFIG_FILE` environment variable.

## Usage

Build and run the server binary:

```bash
go build -o gitduppy ./cmd/server
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

## Web UI

GitDuppy includes a full web interface accessible at `http://localhost:7659` after login.

| Page | URL | Description |
|------|-----|-------------|
| Dashboard | `/dashboard` | Overview of all repositories and recent activity |
| Repository List | `/repos` | Searchable card-grid of all mirrored repositories |
| Repository Browser | `/repos/:id` | Browse files and folders with branch/tag switcher |
| File Viewer | `/repos/:id` (click file) | View file content with syntax highlighting |
| Commit History | `/repos/:id` → View Commit History | Paginated list of commits |
| Commit Detail | `/repos/:id/commit/:sha` | Single commit with full line-by-line diff |
| Settings | `/config` | Application configuration |

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