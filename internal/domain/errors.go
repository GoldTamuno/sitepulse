package domain

import "errors"

// These sentinel errors live in domain, not in the postgres package,
// specifically so the service layer can do errors.Is(err, domain.ErrNotFound)
// without importing anything Postgres-specific. Repository implementations
// (postgres, or any future alternative) are responsible for translating
// their own storage-specific errors (pgx.ErrNoRows, a unique-constraint
// SQLSTATE, etc.) into these before returning — the translation happens at
// the repository boundary, which is exactly where "storage detail" should
// stop leaking into "business logic."
var (
	ErrNotFound = errors.New("domain: resource not found")
	ErrConflict = errors.New("domain: resource already exists")
	// ErrForbidden signals an authorization failure at the business-logic
	// level — e.g. a user attempting to modify a monitor they don't own.
	// Distinct from middleware.RequireRole's 403 (which checks "does this
	// role have this capability at all"): this one checks "does THIS user
	// have the right to touch THIS specific resource," which can only be
	// known once the resource is loaded, not from the role alone.
	ErrForbidden = errors.New("domain: forbidden")
)
