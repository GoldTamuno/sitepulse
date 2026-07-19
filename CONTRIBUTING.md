# Contributing to SitePulse

## Local development setup

See the README's "Local Development" section for the full Docker Compose +
migration setup. Once running:

```bash
go mod tidy
go build ./...
go test ./... -v
```

## Before opening a pull request

Everything below runs automatically in CI (`.github/workflows/ci.yml`) and
will block merge if it fails — running it locally first just saves you a
round trip:

```bash
gofmt -l .              # should print nothing; if it prints files, run: gofmt -w .
go vet ./...
go test -race -cover ./...
go mod tidy && git diff --exit-code go.mod go.sum
```

If you have `golangci-lint` installed locally:
```bash
golangci-lint run
```

## Code conventions

This project follows a layered architecture — see the README's
"Architecture" section before adding new functionality. In short:

- **`domain`** defines entities and repository interfaces. No imports of
  `database/sql`, `net/http`, or any concrete implementation.
- **`service`** holds business logic, depends only on `domain` interfaces
  and sentinel errors — never a concrete `postgres.*` type.
- **`repository/postgres`** implements `domain` interfaces against real
  SQL. This is the only package that should import `pgx`.
- **`handler`** is thin: parse the request, call a service method, map the
  result to JSON. Business logic doesn't belong in a handler.

New business logic should generally land in `service`, with unit tests
against a fake in-memory repository (see `internal/service/fakes_test.go`
for the existing pattern) rather than a real database — this keeps the
test suite fast and dependency-free, and is why CI doesn't run a Postgres
service container at all.

## Database changes

Schema changes are hand-written, numbered SQL migrations under
`migrations/`, applied with `golang-migrate` — never an ORM's
auto-migration. See `migrations/README.md` for the naming convention.
Every migration needs both an `.up.sql` and a `.down.sql`.

## Security-sensitive changes

Anything touching `internal/security`, `internal/middleware/auth.go`, or
password/token handling should be called out explicitly in the PR
description — these are reviewed with extra scrutiny. See `SECURITY.md`
for the project's overall security posture.

## Commit messages

No strict format enforced, but a commit message should explain *why* a
change was made, not just restate the diff — "fix bug" tells a future
reader nothing; "reject already-expired refresh tokens before the reuse
check, not after" does.
