package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/yourname/sitepulse/internal/domain"
	"github.com/yourname/sitepulse/internal/security"
	"github.com/yourname/sitepulse/internal/validation"
)

// newTestAuthService wires an AuthService against fresh fake repos and a
// real TokenIssuer — real, because signing/verifying JWTs is fast, pure,
// and deterministic; faking it would buy us nothing and would mean our
// tests stop covering the actual JWT code path.
func newTestAuthService() (*AuthService, *fakeUserRepo, *fakeRefreshTokenRepo) {
	users := newFakeUserRepo()
	refresh := newFakeRefreshTokenRepo()
	tokens := security.NewTokenIssuer("test-secret-do-not-use-in-prod")
	return NewAuthService(users, refresh, tokens), users, refresh
}

const validPassword = "CorrectHorseBattery123!"

func TestAuthService_Register(t *testing.T) {
	tests := []struct {
		name        string
		email       string
		password    string
		seedEmail   string // if set, a user with this email is created before the test runs
		wantErr     error  // nil means "any non-nil error is fine, just check it's non-nil" is NOT assumed — see wantAnyErr
		wantAnyErr  bool   // true when we only care that SOME error occurred (validation messages are exact-matched separately)
		wantSuccess bool
	}{
		{
			name:        "valid registration succeeds with viewer role",
			email:       "new@example.com",
			password:    validPassword,
			wantSuccess: true,
		},
		{
			name:       "invalid email is rejected",
			email:      "not-an-email",
			password:   validPassword,
			wantErr:    validation.ErrInvalidEmail,
			wantAnyErr: true,
		},
		{
			name:       "password too short is rejected",
			email:      "short@example.com",
			password:   "Ab1!",
			wantErr:    validation.ErrPasswordTooShort,
			wantAnyErr: true,
		},
		{
			name:       "password with only one character class is rejected",
			email:      "weak@example.com",
			password:   "alllowercaseandlong",
			wantErr:    validation.ErrPasswordTooWeak,
			wantAnyErr: true,
		},
		{
			name:       "duplicate email is rejected",
			email:      "dup@example.com",
			password:   validPassword,
			seedEmail:  "dup@example.com",
			wantErr:    ErrEmailTaken,
			wantAnyErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, users, _ := newTestAuthService()
			ctx := context.Background()

			if tt.seedEmail != "" {
				if _, err := svc.Register(ctx, tt.seedEmail, validPassword); err != nil {
					t.Fatalf("seeding existing user failed: %v", err)
				}
			}

			user, err := svc.Register(ctx, tt.email, tt.password)

			if tt.wantAnyErr {
				if err == nil {
					t.Fatalf("expected an error, got none")
				}
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("expected error %v, got %v", tt.wantErr, err)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !tt.wantSuccess {
				return
			}

			if user.Role != domain.RoleViewer {
				t.Errorf("expected role %q, got %q — new registrations must never start with elevated privilege", domain.RoleViewer, user.Role)
			}
			if user.PasswordHash == tt.password {
				t.Errorf("password hash equals raw password — password was not hashed")
			}
			stored, err := users.GetByEmail(ctx, tt.email)
			if err != nil {
				t.Fatalf("user was not actually persisted: %v", err)
			}
			if stored.ID != user.ID {
				t.Errorf("returned user ID %d does not match persisted ID %d", user.ID, stored.ID)
			}
		})
	}
}

func TestAuthService_Login(t *testing.T) {
	ctx := context.Background()

	t.Run("correct credentials issue a token pair", func(t *testing.T) {
		svc, _, _ := newTestAuthService()
		if _, err := svc.Register(ctx, "user@example.com", validPassword); err != nil {
			t.Fatalf("register setup failed: %v", err)
		}

		user, tokens, err := svc.Login(ctx, "user@example.com", validPassword)
		if err != nil {
			t.Fatalf("unexpected login error: %v", err)
		}
		if tokens.AccessToken == "" {
			t.Error("expected a non-empty access token")
		}
		if tokens.RefreshToken == "" {
			t.Error("expected a non-empty refresh token")
		}
		if user.Email != "user@example.com" {
			t.Errorf("expected user email %q, got %q", "user@example.com", user.Email)
		}
	})

	t.Run("wrong password is rejected", func(t *testing.T) {
		svc, _, _ := newTestAuthService()
		if _, err := svc.Register(ctx, "user@example.com", validPassword); err != nil {
			t.Fatalf("register setup failed: %v", err)
		}

		_, _, err := svc.Login(ctx, "user@example.com", "TotallyWrong123!")
		if !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("expected ErrInvalidCredentials, got %v", err)
		}
	})

	t.Run("nonexistent email is rejected with the same error as wrong password", func(t *testing.T) {
		// Deliberately asserting the SAME error as the wrong-password case
		// above — this is what prevents user enumeration. If this test
		// ever required a different error/message for "no such account,"
		// that would itself be a regression worth catching here.
		svc, _, _ := newTestAuthService()

		_, _, err := svc.Login(ctx, "ghost@example.com", validPassword)
		if !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("expected ErrInvalidCredentials, got %v", err)
		}
	})
}

func TestAuthService_Refresh(t *testing.T) {
	ctx := context.Background()

	t.Run("valid token rotates and the old one becomes unusable", func(t *testing.T) {
		svc, _, refreshRepo := newTestAuthService()
		if _, err := svc.Register(ctx, "user@example.com", validPassword); err != nil {
			t.Fatalf("register setup failed: %v", err)
		}
		_, initial, err := svc.Login(ctx, "user@example.com", validPassword)
		if err != nil {
			t.Fatalf("login setup failed: %v", err)
		}

		rotated, err := svc.Refresh(ctx, initial.RefreshToken)
		if err != nil {
			t.Fatalf("unexpected refresh error: %v", err)
		}
		if rotated.RefreshToken == initial.RefreshToken {
			t.Error("refresh token did not change — rotation did not occur")
		}
		if rotated.AccessToken == "" {
			t.Error("expected a non-empty rotated access token")
		}

		// Confirm the original token is now marked revoked in storage —
		// this is what makes reuse detection possible.
		originalHash := security.HashToken(initial.RefreshToken)
		stored, err := refreshRepo.GetByHash(ctx, originalHash)
		if err != nil {
			t.Fatalf("could not look up original token record: %v", err)
		}
		if !stored.Revoked {
			t.Error("original refresh token was not marked revoked after rotation")
		}
	})

	t.Run("reusing an already-rotated token is rejected and revokes the whole family", func(t *testing.T) {
		svc, _, refreshRepo := newTestAuthService()
		if _, err := svc.Register(ctx, "user@example.com", validPassword); err != nil {
			t.Fatalf("register setup failed: %v", err)
		}
		_, initial, err := svc.Login(ctx, "user@example.com", validPassword)
		if err != nil {
			t.Fatalf("login setup failed: %v", err)
		}

		rotated, err := svc.Refresh(ctx, initial.RefreshToken)
		if err != nil {
			t.Fatalf("first refresh should succeed: %v", err)
		}

		// Replay the original (already-spent) token.
		_, err = svc.Refresh(ctx, initial.RefreshToken)
		if !errors.Is(err, ErrTokenReuse) {
			t.Fatalf("expected ErrTokenReuse, got %v", err)
		}

		// The whole family — including the token that was legitimately
		// rotated to next — should now be revoked too, forcing full
		// re-authentication rather than leaving the "next" token usable.
		rotatedHash := security.HashToken(rotated.RefreshToken)
		stored, err := refreshRepo.GetByHash(ctx, rotatedHash)
		if err != nil {
			t.Fatalf("could not look up rotated token record: %v", err)
		}
		if !stored.Revoked {
			t.Error("rotated token was not revoked after reuse of an earlier token in its family")
		}
	})

	t.Run("expired token is rejected", func(t *testing.T) {
		svc, users, refreshRepo := newTestAuthService()
		user := &domain.User{Email: "user@example.com", PasswordHash: "unused", Role: domain.RoleViewer}
		if err := users.Create(ctx, user); err != nil {
			t.Fatalf("seed user failed: %v", err)
		}

		rawToken, err := security.GenerateRefreshToken()
		if err != nil {
			t.Fatalf("generating raw token failed: %v", err)
		}
		expired := &domain.RefreshToken{
			UserID:    user.ID,
			TokenHash: security.HashToken(rawToken),
			FamilyID:  "test-family",
			ExpiresAt: time.Now().Add(-1 * time.Hour), // already expired
			Revoked:   false,
		}
		if err := refreshRepo.Create(ctx, expired); err != nil {
			t.Fatalf("seeding expired token failed: %v", err)
		}

		_, err = svc.Refresh(ctx, rawToken)
		if !errors.Is(err, ErrTokenInvalid) {
			t.Fatalf("expected ErrTokenInvalid, got %v", err)
		}
	})

	t.Run("unknown token is rejected", func(t *testing.T) {
		svc, _, _ := newTestAuthService()

		_, err := svc.Refresh(ctx, "a-token-that-was-never-issued")
		if !errors.Is(err, ErrTokenInvalid) {
			t.Fatalf("expected ErrTokenInvalid, got %v", err)
		}
	})
}

func TestAuthService_Logout(t *testing.T) {
	ctx := context.Background()

	t.Run("logout revokes the token family, blocking future refresh", func(t *testing.T) {
		svc, _, _ := newTestAuthService()
		if _, err := svc.Register(ctx, "user@example.com", validPassword); err != nil {
			t.Fatalf("register setup failed: %v", err)
		}
		_, tokens, err := svc.Login(ctx, "user@example.com", validPassword)
		if err != nil {
			t.Fatalf("login setup failed: %v", err)
		}

		if err := svc.Logout(ctx, tokens.RefreshToken); err != nil {
			t.Fatalf("unexpected logout error: %v", err)
		}

		_, err = svc.Refresh(ctx, tokens.RefreshToken)
		if err == nil {
			t.Fatal("expected refresh to fail after logout, but it succeeded")
		}
	})

	t.Run("logout with an unknown token is idempotent, not an error", func(t *testing.T) {
		svc, _, _ := newTestAuthService()

		if err := svc.Logout(ctx, "never-issued-token"); err != nil {
			t.Fatalf("logout on an unknown token should be a no-op, got error: %v", err)
		}
	})
}
