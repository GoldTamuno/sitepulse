// Package postgres holds concrete implementations of the domain package's
// repository interfaces, plus pool setup. Nothing outside this package
// should ever import pgx directly — that's the whole point of the
// repository pattern: swap Postgres for something else later and only
// this package changes.
package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// NewPool creates a connection pool and verifies connectivity immediately
// via Ping — we want a misconfigured DATABASE_URL or unreachable database
// to crash the app at startup with a clear error, not surface as a
// mysterious 500 on the first user request five minutes later.
func NewPool(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("postgres: parsing DSN: %w", err)
	}

	// MaxConns bounds how many concurrent DB connections this instance can
	// open. Left unbounded, a traffic spike (or a bug causing connection
	// leaks) can exhaust Postgres's own max_connections limit and take
	// down the database for every other service sharing it. 20 is a
	// reasonable default for a single-instance deployment; tune based on
	// actual concurrency needs and Postgres's configured limit.
	cfg.MaxConns = 20
	cfg.MinConns = 2
	cfg.MaxConnLifetime = time.Hour
	cfg.MaxConnIdleTime = 30 * time.Minute
	cfg.HealthCheckPeriod = time.Minute

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("postgres: creating pool: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgres: ping failed: %w", err)
	}

	return pool, nil
}
