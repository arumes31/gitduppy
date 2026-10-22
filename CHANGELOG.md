# Changelog

All notable changes to GitDuppy are documented here. The format is based on
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project aims
to follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Security
- **Repository storage paths** can no longer be changed through the API
  (`storage_path` is ignored on update), and every delete, restore, purge, clone
  and browse operation refuses paths outside `<base_path>/shards/`.
- **Repository permissions**: mutating repository, tag and clone-job routes are
  limited to the repository's creator or an admin. Changing a repository URL to a
  different host requires re-entering its credentials.
- **OAuth sign-up** creates inactive accounts (admin approval) unless
  `oauth.auto_activate` is set, and can be limited with
  `oauth.allowed_email_domains`.
- **SSRF**: webhook deliveries block loopback/private/link-local/metadata
  addresses at dial time and no longer follow redirects; clone URLs resolving
  to loopback/link-local addresses are refused. Opt out with
  `security.allow_private_network_targets`. LFS transfers are pinned to the
  remote's own endpoint so `.lfsconfig` cannot redirect them.
- **Master key**: placeholder or low-entropy keys are refused at startup
  (override: `security.allow_insecure_master_key`); the dev compose file no
  longer ships a default `ENCRYPTION_KEY`.
- **Client IPs**: `X-Forwarded-For` is only trusted from
  `server.trusted_proxies`, closing a rate-limit bypass and audit-log spoofing.
- **CSRF**: cookie-authenticated state-changing requests are checked with
  `Sec-Fetch-Site`/`Origin` (net/http `CrossOriginProtection`); the unused
  gorilla/csrf dependency was removed.
- **Login lockout** is tracked per account and client IP, so the `admin`
  account can no longer be locked out by a third party.
- **Inbound webhooks** without a secret reject deliveries.
- Auth exclusions match exact paths; container base images are pinned by digest
  and the app binary/templates are no longer owned by the runtime user.

### Fixed
- **Storage paths**: legacy repositories whose `StoragePath` predates the
  base-joined layout are normalized (and relocated on disk) at startup, and the
  companion `.wiki` mirror is moved alongside the main tree.
- **Clone retries**: retry jobs are now persisted with a primary key before
  enqueue, so retries actually run instead of silently no-opping.
- **Clone timeouts**: each clone/fetch is now bounded by the configured
  `CloneTimeout`, so a hung remote can no longer pin a worker slot forever.
- **Paperbin size**: computed from the configured storage base instead of a
  hard-coded `repos/` directory.
- **Scheduler**: evaluates repositories every minute (not every 5) and skips
  repositories that already have a pending/running job, preventing duplicate
  clones into the same directory.
- **Pruned-branch records** are written in a single transaction.
- **Dashboard timeline** orders by `COALESCE(started_at, created_at)` so pending
  jobs sort sensibly.

### Added
- **Metrics**: the previously-defined Prometheus collectors are now actually
  updated — clone job counts/duration, active jobs, queue depth, repository
  status gauges, webhook deliveries, and HTTP request counts/latency.
- **Health endpoint** now reports git availability, DB connection-pool stats,
  and the current clone-queue depth.
- **Rate limiting** applies tighter budgets to authentication and expensive
  fan-out endpoints (search, dashboard).
- **Security headers**: static assets are cacheable while dynamic responses stay
  `no-store`.
- **Audit logging** for authentication events (login success/failure, password
  change).
- **Database indexes** for the hot query paths, plus optional pg_trgm trigram
  indexes for repository search.

### Changed
- **Session cookies** are now `SameSite=Lax` and `Secure` over HTTPS.
- **Webhook secrets** are encrypted at rest (AES-256-GCM), transparently
  readable for legacy plaintext values.
- **Global search** scans repositories concurrently under one overall deadline.
- **Dashboard stats** collapse ~13 count queries into 2 grouped aggregates and
  are cached briefly; the storage-size walk is cached and runs off the request
  path.
- **GitHub metadata fetches** share one connection-pooling HTTP client.

### Security
- Removed the ignored, misleading `storage_path` request field; the storage
  location is derived server-side.

---

_Prior history predates this changelog; see the git log._
