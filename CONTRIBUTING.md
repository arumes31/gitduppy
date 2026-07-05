# Contributing to GitDuppy

Thanks for your interest in improving GitDuppy! This guide covers how to get a
development environment running and the expectations for contributions.

## Development setup

Prerequisites: Go 1.26+, Docker (for PostgreSQL), and `git`/`git-lfs`.

```bash
# Start a local PostgreSQL
docker run --rm -d --name gitduppy-pg -p 5433:5432 \
  -e POSTGRES_DB=gitduppy -e POSTGRES_USER=gitduppy -e POSTGRES_PASSWORD=gitduppy \
  postgres:16-alpine

# Point the app at it and run
export GITMIRRORS_DATABASE_HOST=localhost
export GITMIRRORS_DATABASE_PORT=5433
export GITMIRRORS_SECURITY_MASTER_KEY=$(openssl rand -hex 16)
export GITMIRRORS_SECURITY_SESSION_SECRET=$(openssl rand -hex 16)
export GITMIRRORS_SECURITY_CSRF_KEY=$(openssl rand -hex 16)
go run ./cmd/server
```

## Workflow

1. Fork the repository and create a feature branch off `main`.
2. Make your change with a clear, focused commit history.
3. Add or update tests for the behavior you change.
4. Run the checks below and make sure they pass.
5. Open a pull request describing the change and its motivation.

## Required checks

```bash
go build ./...
go vet ./...
go test ./...            # DB-backed tests skip automatically without a database
go test -race ./...      # for anything touching concurrency
```

If you have the linters installed, please also run:

```bash
golangci-lint run
gosec ./...
```

## Guidelines

- Match the style and idioms of the surrounding code.
- Keep changes minimal and well-scoped; prefer generalizing existing mechanisms
  over layering special cases.
- Never log secrets (credentials, tokens, webhook/OAuth secrets).
- Update `CHANGELOG.md` under the `Unreleased` heading for user-visible changes.
- Database schema changes go through `AutoMigrate` plus, where data must be
  transformed, an idempotent migration step.

## Reporting security issues

Please do **not** open a public issue for security vulnerabilities. See
[SECURITY.md](SECURITY.md) for responsible-disclosure instructions.
