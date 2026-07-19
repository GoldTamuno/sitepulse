package domain

import (
	"context"
	"time"
)

// CheckStatus is the outcome of a single health check.
type CheckStatus string

const (
	CheckStatusUp   CheckStatus = "up"
	CheckStatusDown CheckStatus = "down"
	CheckStatusSlow CheckStatus = "slow" // up, but past the latency threshold
)

// Check is one recorded health-check result. This is the high-volume table:
// with N monitors checked every interval, this grows fast, so its repository
// methods are designed around ranges/pagination rather than "get all".
type Check struct {
	ID           int64
	MonitorID    int64
	Status       CheckStatus
	StatusCode   int
	ResponseTime time.Duration
	Error        string // empty on success
	// TLSExpiresAt is the expiry date of the target's TLS certificate, for
	// https:// monitors where a connection was successfully established.
	// nil for http:// monitors, or when the connection never got far
	// enough to negotiate TLS (e.g. connection refused) — there's no
	// certificate to report on in that case. Populated here so Phase 7's
	// alerting logic ("cert expires in <7 days") can query historical
	// checks rather than needing its own separate check mechanism.
	TLSExpiresAt *time.Time
	CheckedAt    time.Time
}

type CheckRepository interface {
	Create(ctx context.Context, c *Check) error
	ListByMonitor(ctx context.Context, monitorID int64, since time.Time, limit int) ([]*Check, error)
	// UptimeSince returns the fraction (0.0-1.0) of checks that were "up"
	// since the given time. Computed in SQL, not in Go, so we're not
	// pulling potentially thousands of rows into memory just to average them.
	UptimeSince(ctx context.Context, monitorID int64, since time.Time) (float64, error)
	AvgResponseTimeSince(ctx context.Context, monitorID int64, since time.Time) (time.Duration, error)
}

// IncidentStatus tracks the lifecycle of an outage.
type IncidentStatus string

const (
	IncidentStatusOpen     IncidentStatus = "open"
	IncidentStatusResolved IncidentStatus = "resolved"
)

// Incident groups consecutive failing checks into a single outage window,
// so the dashboard shows "down for 14 minutes at 3:02am" instead of forty
// individual failed-check rows. The service layer opens one when a check
// transitions up->down, and closes it on the next down->up transition.
type Incident struct {
	ID         int64
	MonitorID  int64
	Status     IncidentStatus
	StartedAt  time.Time
	ResolvedAt *time.Time
	Cause      string
}

type IncidentRepository interface {
	Create(ctx context.Context, i *Incident) error
	GetOpenByMonitor(ctx context.Context, monitorID int64) (*Incident, error)
	Resolve(ctx context.Context, id int64, resolvedAt time.Time) error
	ListByMonitor(ctx context.Context, monitorID int64, limit int) ([]*Incident, error)
}
