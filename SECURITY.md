# Security Policy

## Reporting a vulnerability

If you discover a security vulnerability in GitDuppy, please report it privately
so it can be fixed before public disclosure.

- **Do not** open a public GitHub issue for security problems.
- Email the maintainers with details, or use GitHub's private
  ["Report a vulnerability"](https://docs.github.com/en/code-security/security-advisories/guidance-on-reporting-and-writing-information-about-vulnerabilities/privately-reporting-a-security-vulnerability)
  advisory flow on this repository.
- Include a description, reproduction steps, affected version/commit, and the
  potential impact.

We aim to acknowledge reports within a few business days and to keep you updated
as we investigate and remediate.

## Supported versions

Security fixes are applied to the latest released version and `main`. Older
versions are not maintained.

## Handling of secrets

GitDuppy is designed to keep sensitive material protected:

- Repository credentials and webhook secrets are encrypted at rest with
  AES-256-GCM using the configured master key.
- Administrator passwords are stored only as bcrypt hashes.
- The initial admin password is never written to logs unless an operator
  explicitly opts in for a one-time display.
- Session cookies are `HttpOnly`, `SameSite=Lax`, and `Secure` over HTTPS.

## Hardening recommendations

- Run behind TLS (directly or via the provided Caddy reverse proxy).
- Provide strong, unique values for `GITMIRRORS_SECURITY_MASTER_KEY`,
  `GITMIRRORS_SECURITY_SESSION_SECRET`, and `GITMIRRORS_SECURITY_CSRF_KEY`.
- Supply `GITMIRRORS_BOOTSTRAP_ADMIN_PASSWORD` and change it immediately after
  first login.
- Restrict network exposure of the PostgreSQL instance.
