package domain

import (
	"context"
	"time"
)

// MonitorType distinguishes what kind of check we run against a target.
type MonitorType string

const (
	MonitorTypeHTTP MonitorType = "http"
	MonitorTypeTCP  MonitorType = "tcp"
)

// Monitor is a registered website/API that SitePulse watches.
//
// Note this struct has zero knowledge of Postgres, JSON tags aside — it's a
// pure domain type. The repository layer maps it to/from SQL rows, and the
// handler layer maps it to/from JSON. Neither of those concerns belongs here.
type Monitor struct {
	ID                 int64
	UserID             int64
	Name               string
	URL                string
	Type               MonitorType
	IntervalSeconds    int
	TimeoutSeconds     int
	ExpectedStatusCode int
	Active             bool
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

// MonitorRepository is the persistence contract the service layer depends
// on. It is defined here, in domain, not in the postgres package — this is
// the "dependency inversion" half of clean architecture: the low-level
// detail (Postgres) depends on the interface defined by the high-level
// policy (domain), not the other way around.
type MonitorRepository interface {
	Create(ctx context.Context, m *Monitor) error
	GetByID(ctx context.Context, id int64) (*Monitor, error)
	ListByUser(ctx context.Context, userID int64) ([]*Monitor, error)
	ListActive(ctx context.Context) ([]*Monitor, error) // used by the scheduler
	Update(ctx context.Context, m *Monitor) error
	Delete(ctx context.Context, id int64) error
}
