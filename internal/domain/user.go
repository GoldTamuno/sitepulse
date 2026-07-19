package domain

import (
	"context"
	"time"
)

type Role string

// Three-tier RBAC. Enforced server-side on every request that touches
// protected resources — role claims in a JWT are a hint for routing, not
// a substitute for checking the database-backed source of truth on
// sensitive operations. Frontend role checks are UX only; the backend
// middleware is the actual authorization boundary.
const (
	RoleAdmin    Role = "admin"    // manages users, system-wide settings
	RoleOperator Role = "operator" // creates/edits/deletes monitors
	RoleViewer   Role = "viewer"   // read-only: dashboards, check history
)

type User struct {
	ID            int64
	Email         string
	PasswordHash  string
	Role          Role
	EmailVerified bool
	CreatedAt     time.Time
}

type UserRepository interface {
	Create(ctx context.Context, u *User) error
	GetByEmail(ctx context.Context, email string) (*User, error)
	GetByID(ctx context.Context, id int64) (*User, error)
	// List, UpdateRole, and CountByRole support admin user management
	// (Phase 8) — everything above this existed for auth alone.
	List(ctx context.Context) ([]*User, error)
	UpdateRole(ctx context.Context, id int64, role Role) error
	// CountByRole backs the "don't let the last admin demote themselves"
	// safety check — computed in SQL rather than loading every user into
	// Go just to count one field.
	CountByRole(ctx context.Context, role Role) (int, error)
}

// RefreshToken backs rotation-based refresh auth. We never store the raw
// refresh token — only a SHA-256 hash of it — for the same reason we never
// store raw passwords: if the `refresh_tokens` table ever leaks (backup
// exposure, SQL injection elsewhere, insider access), the leaked hashes are
// useless without also compromising the tokens' issuing secret-holder
// (the client), because a hash can't be turned back into a usable token.
//
// "Rotation" means every refresh exchange issues a brand-new refresh token
// and immediately invalidates the old one (Revoked = true), rather than
// reusing the same long-lived token indefinitely. This bounds the damage
// window of a stolen refresh token: it's single-use, and reuse detection
// (someone presenting an already-revoked token) is a strong signal of
// theft — at which point we revoke the entire token family, not just the
// one token, forcing re-login everywhere.
type RefreshToken struct {
	ID        int64
	UserID    int64
	TokenHash string // SHA-256 hex of the raw token; raw value only ever exists client-side
	FamilyID  string // shared across a chain of rotations; lets us revoke a whole chain on reuse detection
	ExpiresAt time.Time
	Revoked   bool
	CreatedAt time.Time
}

type RefreshTokenRepository interface {
	Create(ctx context.Context, t *RefreshToken) error
	GetByHash(ctx context.Context, tokenHash string) (*RefreshToken, error)
	RevokeFamily(ctx context.Context, familyID string) error
	Revoke(ctx context.Context, id int64) error
	DeleteExpired(ctx context.Context) (int64, error) // called periodically to keep the table small
}
