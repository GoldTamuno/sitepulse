package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yourname/sitepulse/internal/domain"
)

type CheckRepository struct {
	pool *pgxpool.Pool
}

func NewCheckRepository(pool *pgxpool.Pool) *CheckRepository {
	return &CheckRepository{pool: pool}
}

// Create persists one check result. response_time_ms is stored as a plain
// integer (see migration 000003's comment) — converting the Go
// time.Duration to milliseconds happens at this boundary, the one place
// that should know checks are stored with millisecond precision.
func (r *CheckRepository) Create(ctx context.Context, c *domain.Check) error {
	const q = `
		INSERT INTO checks (monitor_id, status, status_code, response_time_ms, error, tls_expires_at, checked_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id`

	var statusCode *int
	if c.StatusCode != 0 {
		statusCode = &c.StatusCode
	}
	var errText *string
	if c.Error != "" {
		errText = &c.Error
	}

	err := r.pool.QueryRow(
		ctx, q,
		c.MonitorID, c.Status, statusCode, c.ResponseTime.Milliseconds(), errText, c.TLSExpiresAt, c.CheckedAt,
	).Scan(&c.ID)
	if err != nil {
		return fmt.Errorf("postgres: create check: %w", err)
	}
	return nil
}

func (r *CheckRepository) ListByMonitor(ctx context.Context, monitorID int64, since time.Time, limit int) ([]*domain.Check, error) {
	const q = `
		SELECT id, monitor_id, status, status_code, response_time_ms, error, tls_expires_at, checked_at
		FROM checks
		WHERE monitor_id = $1 AND checked_at >= $2
		ORDER BY checked_at DESC
		LIMIT $3`

	rows, err := r.pool.Query(ctx, q, monitorID, since, limit)
	if err != nil {
		return nil, fmt.Errorf("postgres: list checks: %w", err)
	}
	defer rows.Close()

	var checks []*domain.Check
	for rows.Next() {
		c := &domain.Check{}
		var responseTimeMs int64
		var statusCode *int
		var errText *string

		if err := rows.Scan(&c.ID, &c.MonitorID, &c.Status, &statusCode, &responseTimeMs, &errText, &c.TLSExpiresAt, &c.CheckedAt); err != nil {
			return nil, fmt.Errorf("postgres: scan check row: %w", err)
		}
		if statusCode != nil {
			c.StatusCode = *statusCode
		}
		if errText != nil {
			c.Error = *errText
		}
		c.ResponseTime = time.Duration(responseTimeMs) * time.Millisecond

		checks = append(checks, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: iterating check rows: %w", err)
	}
	return checks, nil
}

// UptimeSince computes the fraction of checks that were NOT "down" since a
// given time, entirely in SQL. Two deliberate choices here:
//
//  1. This is computed with SQL aggregates (COUNT/FILTER), not by pulling
//     every row into Go and counting in a loop — for a monitor checked
//     every 30s over a 30-day window, that's ~86,000 rows to transfer and
//     hold in memory just to compute one float. The database is far better
//     positioned to do this aggregation next to the data.
//  2. "slow" counts as uptime, "down" does not. A slow-but-successful
//     response means the target was reachable and functioning, just
//     sluggish — conflating that with a genuine outage would understate
//     uptime for a site that's simply having a rough but working day.
func (r *CheckRepository) UptimeSince(ctx context.Context, monitorID int64, since time.Time) (float64, error) {
	const q = `
		SELECT
			CASE WHEN COUNT(*) = 0 THEN 0
			ELSE COUNT(*) FILTER (WHERE status <> 'down')::float8 / COUNT(*)::float8
			END
		FROM checks
		WHERE monitor_id = $1 AND checked_at >= $2`

	var uptime float64
	if err := r.pool.QueryRow(ctx, q, monitorID, since).Scan(&uptime); err != nil {
		return 0, fmt.Errorf("postgres: compute uptime: %w", err)
	}
	return uptime, nil
}

// AvgResponseTimeSince excludes "down" checks from the average
// deliberately: a down check's recorded response time is either near-zero
// (connection refused, failed instantly) or equal to the full timeout
// (a hang), and either way it doesn't represent real service latency —
// including it would skew the average toward a number that describes
// failure behavior, not performance.
func (r *CheckRepository) AvgResponseTimeSince(ctx context.Context, monitorID int64, since time.Time) (time.Duration, error) {
	const q = `
		SELECT COALESCE(AVG(response_time_ms), 0)
		FROM checks
		WHERE monitor_id = $1 AND checked_at >= $2 AND status <> 'down'`

	var avgMs float64
	if err := r.pool.QueryRow(ctx, q, monitorID, since).Scan(&avgMs); err != nil {
		return 0, fmt.Errorf("postgres: compute average response time: %w", err)
	}
	return time.Duration(avgMs) * time.Millisecond, nil
}
