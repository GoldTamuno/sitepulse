// Package service holds business logic. Crucially, this package imports
// only domain interfaces and sentinel errors — never concrete postgres or
// security-implementation types by name — which is what lets us unit test
// AuthService against fake in-memory repositories without a real database
// or real crypto in the test suite.
package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/yourname/sitepulse/internal/domain"
	"github.com/yourname/sitepulse/internal/security"
	"github.com/yourname/sitepulse/internal/validation"
)

var (
	ErrEmailTaken         = errors.New("service: email already registered")
	ErrInvalidCredentials = errors.New("service: invalid email or password")
	ErrTokenReuse         = errors.New("service: refresh token reuse detected, session revoked")
	ErrTokenInvalid       = errors.New("service: refresh token invalid or expired")
)

// AuthTokens is what we hand back after a successful auth operation: an
// access token for the client to send on every request, and a raw refresh
// token for the client to store (as an httpOnly cookie — see the handler).
type AuthTokens struct {
	AccessToken  string
	RefreshToken string
	// RefreshExpiresAt is the refresh token's expiry, used only for setting
	// the cookie's Expires attribute. It is NOT the access token's expiry —
	// that's the fixed security.AccessTokenTTL constant, computed
	// separately in the handler. Conflating these two was a real bug
	// caught during manual testing: the /refresh endpoint was reporting
	// "expires_in: 604799" (~7 days) instead of 900 (15 min) because it
	// read this field expecting access-token expiry. Named explicitly now
	// so that mistake can't silently happen again.
	RefreshExpiresAt time.Time
}

type AuthService struct {
	users    domain.UserRepository
	refresh  domain.RefreshTokenRepository
	tokens   *security.TokenIssuer
}

func NewAuthService(users domain.UserRepository, refresh domain.RefreshTokenRepository, tokens *security.TokenIssuer) *AuthService {
	return &AuthService{users: users, refresh: refresh, tokens: tokens}
}

// Register creates a new user with the least-privileged role. Role
// escalation (making someone an operator or admin) is a separate,
// admin-only operation added in the user-management phase — never
// something a registration payload can request for itself. If it were,
// anyone could self-register as "admin":"true" in the JSON body.
func (s *AuthService) Register(ctx context.Context, email, password string) (*domain.User, error) {
	if err := validation.ValidateEmail(email); err != nil {
		return nil, err
	}
	if err := validation.ValidatePassword(password); err != nil {
		return nil, err
	}

	hash, err := security.HashPassword(password)
	if err != nil {
		return nil, fmt.Errorf("service: hashing password: %w", err)
	}

	user := &domain.User{
		Email:         email,
		PasswordHash:  hash,
		Role:          domain.RoleViewer,
		EmailVerified: false,
	}

	if err := s.users.Create(ctx, user); err != nil {
		if errors.Is(err, domain.ErrConflict) {
			return nil, ErrEmailTaken
		}
		return nil, fmt.Errorf("service: creating user: %w", err)
	}

	return user, nil
}

// Login verifies credentials and issues a fresh token pair.
//
// Note the response to "email not found" and "wrong password" is
// identical (ErrInvalidCredentials) and takes a comparable amount of time
// either way — we still run VerifyPassword against a dummy hash when the
// user doesn't exist, rather than returning early. Without that, an
// attacker could distinguish "no such account" from "wrong password" by
// response time alone, which is a user-enumeration vector.
func (s *AuthService) Login(ctx context.Context, email, password string) (*domain.User, *AuthTokens, error) {
	user, err := s.users.GetByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			// Burn roughly the same time a real verification would take.
			_, _ = security.VerifyPassword(password, dummyHash)
			return nil, nil, ErrInvalidCredentials
		}
		return nil, nil, fmt.Errorf("service: looking up user: %w", err)
	}

	match, err := security.VerifyPassword(password, user.PasswordHash)
	if err != nil {
		return nil, nil, fmt.Errorf("service: verifying password: %w", err)
	}
	if !match {
		return nil, nil, ErrInvalidCredentials
	}

	tokens, err := s.issueTokenPair(ctx, user, uuid.NewString())
	if err != nil {
		return nil, nil, err
	}
	return user, tokens, nil
}

// dummyHash is a precomputed Argon2id hash with no known matching
// password, used purely to keep Login's timing consistent when a user
// doesn't exist. Generated once at package init.
var dummyHash string

func init() {
	h, err := security.HashPassword("dummy-password-for-timing-parity-only")
	if err != nil {
		panic("service: failed to precompute dummy hash: " + err.Error())
	}
	dummyHash = h
}

// Refresh implements rotate-on-use with reuse detection.
//
// Flow: client sends its current refresh token -> we look up its hash ->
// if it's already revoked, that means either (a) it was already used to
// refresh once, and this is a legitimate replay we should just ignore, or
// (b) it was stolen and both the thief and the legitimate client are now
// racing to use it, which is an attack. We can't fully distinguish these
// from the token alone, so we take the safe path: treat any presentation
// of an already-revoked token as a reuse signal and revoke the entire
// family, forcing everyone (attacker included) to re-authenticate. This
// trades a small chance of inconveniencing a legitimate client (e.g. a
// retried request racing a real rotation) for closing the much worse case
// of a stolen token granting silent long-term access.
func (s *AuthService) Refresh(ctx context.Context, rawToken string) (*AuthTokens, error) {
	hash := security.HashToken(rawToken)

	stored, err := s.refresh.GetByHash(ctx, hash)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, ErrTokenInvalid
		}
		return nil, fmt.Errorf("service: looking up refresh token: %w", err)
	}

	if stored.Revoked {
		if revokeErr := s.refresh.RevokeFamily(ctx, stored.FamilyID); revokeErr != nil {
			return nil, fmt.Errorf("service: revoking token family after reuse: %w", revokeErr)
		}
		return nil, ErrTokenReuse
	}

	if time.Now().After(stored.ExpiresAt) {
		return nil, ErrTokenInvalid
	}

	user, err := s.users.GetByID(ctx, stored.UserID)
	if err != nil {
		return nil, fmt.Errorf("service: looking up user for refresh: %w", err)
	}

	// Revoke the token being used before issuing its replacement — if
	// issueTokenPair fails partway, we've failed closed (old token dead,
	// no new token) rather than failed open (old token still valid AND a
	// new one issued, doubling the live token count).
	if err := s.refresh.Revoke(ctx, stored.ID); err != nil {
		return nil, fmt.Errorf("service: revoking used refresh token: %w", err)
	}

	return s.issueTokenPair(ctx, user, stored.FamilyID)
}

// Logout revokes the entire family tied to the presented refresh token —
// not just the one token — so that "log out" actually invalidates the
// session, including any tokens already rotated forward from it.
func (s *AuthService) Logout(ctx context.Context, rawToken string) error {
	hash := security.HashToken(rawToken)
	stored, err := s.refresh.GetByHash(ctx, hash)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil // already gone; logout is idempotent
		}
		return fmt.Errorf("service: looking up token for logout: %w", err)
	}
	return s.refresh.RevokeFamily(ctx, stored.FamilyID)
}

func (s *AuthService) issueTokenPair(ctx context.Context, user *domain.User, familyID string) (*AuthTokens, error) {
	access, err := s.tokens.GenerateAccessToken(user.ID, user.Role)
	if err != nil {
		return nil, fmt.Errorf("service: generating access token: %w", err)
	}

	rawRefresh, err := security.GenerateRefreshToken()
	if err != nil {
		return nil, fmt.Errorf("service: generating refresh token: %w", err)
	}

	expiresAt := time.Now().Add(security.RefreshTokenTTL)
	record := &domain.RefreshToken{
		UserID:    user.ID,
		TokenHash: security.HashToken(rawRefresh),
		FamilyID:  familyID,
		ExpiresAt: expiresAt,
		Revoked:   false,
	}
	if err := s.refresh.Create(ctx, record); err != nil {
		return nil, fmt.Errorf("service: storing refresh token: %w", err)
	}

	return &AuthTokens{
		AccessToken:       access,
		RefreshToken:      rawRefresh,
		RefreshExpiresAt:  expiresAt,
	}, nil
}
