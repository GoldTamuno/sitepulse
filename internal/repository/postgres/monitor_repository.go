package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yourname/sitepulse/internal/domain"
)

type MonitorRepository struct {
	pool *pgxpool.Pool
}

func NewMonitorRepository(pool *pgxpool.Pool) *MonitorRepository {
	return &MonitorRepository{pool: pool}
}

func (r *MonitorRepository) Create(ctx context.Context, m *domain.Monitor) error {
	const q = `
		INSERT INTO monitors (user_id, name, url, type, interval_seconds, timeout_seconds, expected_status_code, active)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id, created_at, updated_at`

	err := r.pool.QueryRow(ctx, q,
		m.UserID, m.Name, m.URL, m.Type, m.IntervalSeconds, m.TimeoutSeconds, m.ExpectedStatusCode, m.Active,
	).Scan(&m.ID, &m.CreatedAt, &m.UpdatedAt)
	if err != nil {
		return fmt.Errorf("postgres: create monitor: %w", err)
	}
	return nil
}

func (r *MonitorRepository) GetByID(ctx context.Context, id int64) (*domain.Monitor, error) {
	const q = `
		SELECT id, user_id, name, url, type, interval_seconds, timeout_seconds, expected_status_code, active, created_at, updated_at
		FROM monitors WHERE id = $1`

	m := &domain.Monitor{}
	err := r.pool.QueryRow(ctx, q, id).Scan(
		&m.ID, &m.UserID, &m.Name, &m.URL, &m.Type, &m.IntervalSeconds, &m.TimeoutSeconds,
		&m.ExpectedStatusCode, &m.Active, &m.CreatedAt, &m.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("postgres: get monitor: %w", err)
	}
	return m, nil
}

func (r *MonitorRepository) ListByUser(ctx context.Context, userID int64) ([]*domain.Monitor, error) {
	const q = `
		SELECT id, user_id, name, url, type, interval_seconds, timeout_seconds, expected_status_code, active, created_at, updated_at
		FROM monitors WHERE user_id = $1 ORDER BY created_at DESC`

	rows, err := r.pool.Query(ctx, q, userID)
	if err != nil {
		return nil, fmt.Errorf("postgres: list monitors by user: %w", err)
	}
	defer rows.Close()

	return scanMonitors(rows)
}

// ListActive is the query the Phase 4 scheduler will poll on every tick —
// "which monitors are due for a check." Only active=true monitors are
// returned; a user pausing a monitor should make it disappear from this
// query immediately, not just get skipped after being loaded.
func (r *MonitorRepository) ListActive(ctx context.Context) ([]*domain.Monitor, error) {
	const q = `
		SELECT id, user_id, name, url, type, interval_seconds, timeout_seconds, expected_status_code, active, created_at, updated_at
		FROM monitors WHERE active = true`

	rows, err := r.pool.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("postgres: list active monitors: %w", err)
	}
	defer rows.Close()

	return scanMonitors(rows)
}

func (r *MonitorRepository) Update(ctx context.Context, m *domain.Monitor) error {
	const q = `
		UPDATE monitors
		SET name = $1, url = $2, type = $3, interval_seconds = $4, timeout_seconds = $5,
		    expected_status_code = $6, active = $7, updated_at = now()
		WHERE id = $8
		RETURNING updated_at`

	err := r.pool.QueryRow(ctx, q,
		m.Name, m.URL, m.Type, m.IntervalSeconds, m.TimeoutSeconds, m.ExpectedStatusCode, m.Active, m.ID,
	).Scan(&m.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ErrNotFound
		}
		return fmt.Errorf("postgres: update monitor: %w", err)
	}
	return nil
}

func (r *MonitorRepository) Delete(ctx context.Context, id int64) error {
	const q = `DELETE FROM monitors WHERE id = $1`
	tag, err := r.pool.Exec(ctx, q, id)
	if err != nil {
		return fmt.Errorf("postgres: delete monitor: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// scanMonitors is shared between ListByUser and ListActive — both run the
// identical column set, just with a different WHERE clause, so the
// row-scanning loop is factored out rather than duplicated.
func scanMonitors(rows pgx.Rows) ([]*domain.Monitor, error) {
	var monitors []*domain.Monitor
	for rows.Next() {
		m := &domain.Monitor{}
		if err := rows.Scan(
			&m.ID, &m.UserID, &m.Name, &m.URL, &m.Type, &m.IntervalSeconds, &m.TimeoutSeconds,
			&m.ExpectedStatusCode, &m.Active, &m.CreatedAt, &m.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("postgres: scan monitor row: %w", err)
		}
		monitors = append(monitors, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: iterating monitor rows: %w", err)
	}
	return monitors, nil
}
