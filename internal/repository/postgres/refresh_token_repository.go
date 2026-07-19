package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yourname/sitepulse/internal/domain"
)

type RefreshTokenRepository struct {
	pool *pgxpool.Pool
}

func NewRefreshTokenRepository(pool *pgxpool.Pool) *RefreshTokenRepository {
	return &RefreshTokenRepository{pool: pool}
}

func (r *RefreshTokenRepository) Create(ctx context.Context, t *domain.RefreshToken) error {
	const q = `
		INSERT INTO refresh_tokens (user_id, token_hash, family_id, expires_at, revoked)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, created_at`

	err := r.pool.QueryRow(ctx, q, t.UserID, t.TokenHash, t.FamilyID, t.ExpiresAt, t.Revoked).
		Scan(&t.ID, &t.CreatedAt)
	if err != nil {
		return fmt.Errorf("postgres: create refresh token: %w", err)
	}
	return nil
}

func (r *RefreshTokenRepository) GetByHash(ctx context.Context, tokenHash string) (*domain.RefreshToken, error) {
	const q = `
		SELECT id, user_id, token_hash, family_id, expires_at, revoked, created_at
		FROM refresh_tokens WHERE token_hash = $1`

	t := &domain.RefreshToken{}
	err := r.pool.QueryRow(ctx, q, tokenHash).
		Scan(&t.ID, &t.UserID, &t.TokenHash, &t.FamilyID, &t.ExpiresAt, &t.Revoked, &t.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("postgres: get refresh token: %w", err)
	}
	return t, nil
}

// RevokeFamily marks every token in a rotation chain as revoked. Called
// when we detect reuse of an already-revoked token — the strongest signal
// available that a refresh token was stolen, since a legitimate client
// only ever presents the most recent token in its chain.
func (r *RefreshTokenRepository) RevokeFamily(ctx context.Context, familyID string) error {
	const q = `UPDATE refresh_tokens SET revoked = true WHERE family_id = $1 AND revoked = false`
	if _, err := r.pool.Exec(ctx, q, familyID); err != nil {
		return fmt.Errorf("postgres: revoke family: %w", err)
	}
	return nil
}

func (r *RefreshTokenRepository) Revoke(ctx context.Context, id int64) error {
	const q = `UPDATE refresh_tokens SET revoked = true WHERE id = $1`
	if _, err := r.pool.Exec(ctx, q, id); err != nil {
		return fmt.Errorf("postgres: revoke token: %w", err)
	}
	return nil
}

func (r *RefreshTokenRepository) DeleteExpired(ctx context.Context) (int64, error) {
	const q = `DELETE FROM refresh_tokens WHERE expires_at < now()`
	tag, err := r.pool.Exec(ctx, q)
	if err != nil {
		return 0, fmt.Errorf("postgres: delete expired tokens: %w", err)
	}
	return tag.RowsAffected(), nil
}
