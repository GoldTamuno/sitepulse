package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yourname/sitepulse/internal/domain"
)

type UserRepository struct {
	pool *pgxpool.Pool
}

func NewUserRepository(pool *pgxpool.Pool) *UserRepository {
	return &UserRepository{pool: pool}
}

func (r *UserRepository) Create(ctx context.Context, u *domain.User) error {
	const q = `
		INSERT INTO users (email, password_hash, role, email_verified)
		VALUES ($1, $2, $3, $4)
		RETURNING id, created_at`

	err := r.pool.QueryRow(ctx, q, u.Email, u.PasswordHash, u.Role, u.EmailVerified).
		Scan(&u.ID, &u.CreatedAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return domain.ErrConflict
		}
		return fmt.Errorf("postgres: create user: %w", err)
	}
	return nil
}

func (r *UserRepository) GetByEmail(ctx context.Context, email string) (*domain.User, error) {
	const q = `
		SELECT id, email, password_hash, role, email_verified, created_at
		FROM users WHERE LOWER(email) = LOWER($1)`

	u := &domain.User{}
	err := r.pool.QueryRow(ctx, q, email).
		Scan(&u.ID, &u.Email, &u.PasswordHash, &u.Role, &u.EmailVerified, &u.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("postgres: get user by email: %w", err)
	}
	return u, nil
}

func (r *UserRepository) GetByID(ctx context.Context, id int64) (*domain.User, error) {
	const q = `
		SELECT id, email, password_hash, role, email_verified, created_at
		FROM users WHERE id = $1`

	u := &domain.User{}
	err := r.pool.QueryRow(ctx, q, id).
		Scan(&u.ID, &u.Email, &u.PasswordHash, &u.Role, &u.EmailVerified, &u.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("postgres: get user by id: %w", err)
	}
	return u, nil
}

func (r *UserRepository) List(ctx context.Context) ([]*domain.User, error) {
	const q = `
		SELECT id, email, password_hash, role, email_verified, created_at
		FROM users ORDER BY created_at ASC`

	rows, err := r.pool.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("postgres: list users: %w", err)
	}
	defer rows.Close()

	var users []*domain.User
	for rows.Next() {
		u := &domain.User{}
		if err := rows.Scan(&u.ID, &u.Email, &u.PasswordHash, &u.Role, &u.EmailVerified, &u.CreatedAt); err != nil {
			return nil, fmt.Errorf("postgres: scan user row: %w", err)
		}
		users = append(users, u)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: iterating user rows: %w", err)
	}
	return users, nil
}

func (r *UserRepository) UpdateRole(ctx context.Context, id int64, role domain.Role) error {
	const q = `UPDATE users SET role = $1 WHERE id = $2`
	tag, err := r.pool.Exec(ctx, q, role, id)
	if err != nil {
		return fmt.Errorf("postgres: update user role: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (r *UserRepository) CountByRole(ctx context.Context, role domain.Role) (int, error) {
	const q = `SELECT COUNT(*) FROM users WHERE role = $1`
	var count int
	if err := r.pool.QueryRow(ctx, q, role).Scan(&count); err != nil {
		return 0, fmt.Errorf("postgres: count users by role: %w", err)
	}
	return count, nil
}
