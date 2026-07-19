# Security Policy

SitePulse is a portfolio/production-grade project built with security as a
first-class concern, not an afterthought. This document describes the
security measures in place and how to report a vulnerability.

## Reporting a Vulnerability

If you find a security issue, please open a private security advisory on
GitHub (Security tab → "Report a vulnerability") rather than a public issue.

## Authentication & Authorization

- Passwords hashed with **Argon2id** (memory-hard, OWASP-recommended
  default), never stored or logged in plaintext.
- **JWT access tokens** (15 min TTL) + **rotating refresh tokens** (7 day
  TTL, single-use, stored server-side only as SHA-256 hashes). Refresh
  token reuse (a revoked token presented again) revokes the entire token
  family, forcing re-authentication — this is the primary defense against
  a stolen refresh token being used silently alongside the legitimate user.
- Three-tier RBAC (**admin / operator / viewer**), enforced server-side on
  every protected route via middleware — never inferred from client input.

## Secrets

- No secrets are committed to this repository. `.env` is gitignored;
  `.env.example` documents required variables without real values.
- In production (Railway), secrets are injected as environment variables
  via the platform's secret management, not baked into images.

## Data Handling

- All SQL is parameterized (via `pgx`) — no string-concatenated queries.
- Structured logs never include passwords, tokens, or `Authorization`
  headers.
- API error responses are generic and safe by default; full error detail
  (stack traces, DB errors) is logged server-side only, never returned to
  clients.

## Dependency & Supply Chain

- `go mod tidy` / `go mod verify` run on every push and pull request via
  GitHub Actions (`.github/workflows/ci.yml`), and fail the check if
  `go.mod`/`go.sum` aren't clean.
- `govulncheck ./...` runs in CI, cross-referencing this module's actual
  call graph against Go's public vulnerability database — a dependency is
  only flagged if the vulnerable code path is genuinely reachable from
  this codebase.
- Secret scanning (gitleaks) runs on every push/PR, scanning full git
  history, not just the latest commit.
- Dependabot opens automated PRs for outdated Go modules, the Docker base
  image, and GitHub Actions versions — each one runs through the same CI
  gate as a human-authored PR before merge.
- A CycloneDX SBOM is generated on every build via Syft and uploaded as a
  CI artifact.

## Transport & HTTP Hardening

- CORS is configured with an explicit allowlist in production (not `*`).
- Standard security headers (HSTS, X-Content-Type-Options, X-Frame-Options,
  Referrer-Policy) are applied — see `internal/middleware`.

## Rate Limiting

Authentication endpoints (register, login, refresh, password reset) are
rate-limited to mitigate credential stuffing and brute-force attacks.

---

This document is updated as the project evolves — see the README roadmap
for what's implemented vs. planned.
