# SitePulse

A website & API uptime monitoring platform, built in Go to showcase
concurrency, networking, and production backend engineering — not CRUD.

SitePulse periodically checks registered endpoints in the background,
records response times and status codes, computes uptime, detects outages
and SSL expiry, and can alert via email/webhook.

## Status

🚧 Phase 1 complete: project skeleton, config, structured logging, graceful
shutdown, and the base HTTP server are wired up. Everything else is stubbed
with comments marking where each phase plugs in.

## Architecture

```
                    ┌─────────────┐
   HTTP request ──► │   handler   │  thin: parse request, call service, write response
                    └──────┬──────┘
                           │ depends on
                    ┌──────▼──────┐
                    │   service   │  business logic (uptime calc, incident open/close)
                    └──────┬──────┘
                           │ depends on interfaces defined in
                    ┌──────▼──────┐
                    │   domain    │  entities + repository interfaces, zero external deps
                    └──────▲──────┘
                           │ implemented by
                    ┌──────┴──────┐
                    │  postgres   │  concrete repository implementations (SQL)
                    └─────────────┘

   ┌────────────┐      ┌─────────────┐      ┌───────────┐
   │ scheduler  │ ───► │ worker pool │ ───► │  checker  │  concurrency core:
   │  (ticker)  │      │ (goroutines)│      │(HTTP call)│  ticker finds due monitors,
   └────────────┘      └─────────────┘      └───────────┘  fans work to N goroutines,
                                                             each check has its own timeout
```

**Dependency direction always points inward**, toward `domain`. `service`
never imports `database/sql` or `net/http` directly — it depends on
interfaces (`MonitorRepository`, `CheckRepository`) that `domain` defines
and `postgres` implements. This is what makes `service` unit-testable with
a fake in-memory repository instead of a real database, and what makes the
codebase safe to evolve (e.g. swap Postgres for something else) without
touching business logic.

## Why Go

- **Goroutines + worker pools**: checking hundreds of endpoints on a
  schedule is an embarrassingly parallel I/O problem — exactly what Go's
  concurrency model is built for.
- **context.Context**: every check gets a bounded timeout and can be
  cancelled cleanly on shutdown, propagated from the top-level scheduler
  down to the individual HTTP call.
- **Structured concurrency primitives** (channels, WaitGroups, mutexes):
  used deliberately, each solving a specific coordination problem — worker
  pool fan-out, safe shared-state updates, graceful drain on shutdown —
  not sprinkled in for show.

## Tech Stack

- Go 1.22, [Chi](https://github.com/go-chi/chi) router
- PostgreSQL via [pgx](https://github.com/jackc/pgx)
- Docker / Docker Compose, deployed to Railway
- JWT auth, Swagger/OpenAPI docs
- `log/slog` structured logging
- Table-driven tests with Go's stdlib `testing`

## Project Layout

```
cmd/api/            entrypoint — wires config, logger, DB, router, graceful shutdown
internal/config/     env-driven configuration, validated at startup
internal/logger/     structured logging setup (slog)
internal/domain/     entities + repository interfaces (no external deps)
internal/service/    business logic (added Phase 3+)
internal/repository/postgres/  SQL implementations of domain interfaces (Phase 2)
internal/handler/    HTTP handlers (thin — parse, call service, respond)
internal/middleware/ Chi middleware (logging done; auth/rate-limit later)
internal/server/     router construction, middleware chain assembly
internal/scheduler/  ticker-driven background job that finds due monitors (Phase 4)
internal/checker/    bounded worker pool that performs the actual HTTP checks (Phase 5)
migrations/          hand-written numbered SQL migrations (golang-migrate)
deployments/         Dockerfile, docker-compose.yml
docs/                pointer to the OpenAPI spec + how to view it (spec itself lives in internal/handler/, see docs/README.md)
```

## Local Development

```bash
cp .env.example .env
make docker-up      # Postgres + API via Docker Compose
# or, running the API directly against a local Postgres:
make run
```

## Roadmap

1. ✅ Project setup and architecture
2. ✅ Authentication — Argon2id password hashing, JWT access tokens,
   rotating refresh tokens, RBAC (admin/operator/viewer)
3. ✅ Website management (CRUD for monitors, ownership enforcement)
4. ✅ Concurrent monitoring engine (bounded worker pool, ticker scheduler,
   retries for transient failures, graceful shutdown)
5. ✅ Historical monitoring data (checks persisted to Postgres, TLS expiry
   capture, uptime % and avg response time computed in SQL)
6. ✅ Dashboard (summary, per-monitor stats/history, incident detection
   and history)
7. ✅ Notifications (email + webhook alerts, pluggable notifier chain,
   SSL expiry warnings with de-duplication)
8. ✅ User & admin management (role promotion via API, self-demotion and
   last-admin protection — replaces the raw SQL UPDATE used during earlier
   testing)
9. ✅ API documentation (hand-written OpenAPI 3.0 spec, served as an
   interactive Swagger UI at /docs — see docs/README.md)
10. ✅ CI/CD + supply chain security (GitHub Actions: gofmt/vet/lint,
    race-enabled tests, govulncheck, gitleaks, go.mod/go.sum verification,
    Docker build, CycloneDX SBOM via Syft, Dependabot for Go modules/
    Docker base images/Actions versions)
11. Deployment (Railway, environment-specific config, migration-on-release)

See `SECURITY.md` for the project's security posture in detail.

> **Note on scope**: the original plan had 8 phases. Building it revealed
> real, separable work the 8-phase list undersold — notably user/admin
> management (role changes currently require a direct SQL UPDATE, a
> testing shortcut not a feature) and documentation/CI, both explicitly
> wanted but not accounted for as their own phases. 11 is the honest count.

> **One manual step CI can't do for itself**: the workflow in
> `.github/workflows/ci.yml` runs on every push/PR, but GitHub only
> actually *blocks* merging on a failing check once branch protection is
> turned on for `main` (Settings → Branches → Branch protection rules →
> require the `lint`, `test`, `build`, `deps`, `vulncheck`, `secrets`
> checks to pass). That's a one-time setting in the GitHub UI, not
> something a YAML file can enable on its own.
