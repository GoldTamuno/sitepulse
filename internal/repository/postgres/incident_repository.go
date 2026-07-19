package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yourname/sitepulse/internal/domain"
)

type IncidentRepository struct {
	pool *pgxpool.Pool
}

func NewIncidentRepository(pool *pgxpool.Pool) *IncidentRepository {
	return &IncidentRepository{pool: pool}
}

// Create opens a new incident. If one is already open for this monitor,
// the unique partial index from migration 000004
// (idx_incidents_one_open_per_monitor) rejects the insert with a 23505
// violation — which we treat as a harmless no-op, not an error. This
// matters because check results can, in principle, be recorded slightly
// out of order or with overlapping in-flight goroutines; the database
// constraint is the actual source of truth for "is there already an open
// incident," and this method just needs to not crash when it loses that
// race.
func (r *IncidentRepository) Create(ctx context.Context, i *domain.Incident) error {
	const q = `
		INSERT INTO incidents (monitor_id, status, started_at, cause)
		VALUES ($1, $2, $3, $4)
		RETURNING id`

	err := r.pool.QueryRow(ctx, q, i.MonitorID, i.Status, i.StartedAt, i.Cause).Scan(&i.ID)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil // an incident was already open — not a real error
		}
		return fmt.Errorf("postgres: create incident: %w", err)
	}
	return nil
}

func (r *IncidentRepository) GetOpenByMonitor(ctx context.Context, monitorID int64) (*domain.Incident, error) {
	const q = `
		SELECT id, monitor_id, status, started_at, resolved_at, cause
		FROM incidents WHERE monitor_id = $1 AND status = 'open'`

	i := &domain.Incident{}
	err := r.pool.QueryRow(ctx, q, monitorID).Scan(&i.ID, &i.MonitorID, &i.Status, &i.StartedAt, &i.ResolvedAt, &i.Cause)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("postgres: get open incident: %w", err)
	}
	return i, nil
}

func (r *IncidentRepository) Resolve(ctx context.Context, id int64, resolvedAt time.Time) error {
	const q = `UPDATE incidents SET status = 'resolved', resolved_at = $1 WHERE id = $2 AND status = 'open'`
	if _, err := r.pool.Exec(ctx, q, resolvedAt, id); err != nil {
		return fmt.Errorf("postgres: resolve incident: %w", err)
	}
	return nil
}

func (r *IncidentRepository) ListByMonitor(ctx context.Context, monitorID int64, limit int) ([]*domain.Incident, error) {
	const q = `
		SELECT id, monitor_id, status, started_at, resolved_at, cause
		FROM incidents WHERE monitor_id = $1
		ORDER BY started_at DESC LIMIT $2`

	rows, err := r.pool.Query(ctx, q, monitorID, limit)
	if err != nil {
		return nil, fmt.Errorf("postgres: list incidents: %w", err)
	}
	defer rows.Close()

	var incidents []*domain.Incident
	for rows.Next() {
		i := &domain.Incident{}
		if err := rows.Scan(&i.ID, &i.MonitorID, &i.Status, &i.StartedAt, &i.ResolvedAt, &i.Cause); err != nil {
			return nil, fmt.Errorf("postgres: scan incident row: %w", err)
		}
		incidents = append(incidents, i)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: iterating incident rows: %w", err)
	}
	return incidents, nil
}
