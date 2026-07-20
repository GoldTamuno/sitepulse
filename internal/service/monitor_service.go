package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/yourname/sitepulse/internal/domain"
	"github.com/yourname/sitepulse/internal/validation"
)

// CreateMonitorInput and UpdateMonitorInput are service-layer DTOs, not
// domain types. domain.Monitor represents "what a monitor IS"; these
// represent "what a client is allowed to submit to create/change one" —
// deliberately not the same shape (e.g. a client can't set UserID,
// CreatedAt, or ID directly). Keeping these in service rather than domain
// keeps domain free of anything shaped by HTTP/API concerns.
type CreateMonitorInput struct {
	Name               string
	URL                string
	Type               domain.MonitorType
	IntervalSeconds    int
	TimeoutSeconds     int
	ExpectedStatusCode int
}

type UpdateMonitorInput struct {
	Name               string
	URL                string
	Type               domain.MonitorType
	IntervalSeconds    int
	TimeoutSeconds     int
	ExpectedStatusCode int
	Active             bool
}

var ErrInvalidMonitorType = errors.New("service: type must be 'http' or 'tcp'")

type MonitorService struct {
	monitors  domain.MonitorRepository
	checks    domain.CheckRepository
	incidents domain.IncidentRepository
}

func NewMonitorService(monitors domain.MonitorRepository, checks domain.CheckRepository, incidents domain.IncidentRepository) *MonitorService {
	return &MonitorService{monitors: monitors, checks: checks, incidents: incidents}
}

func (s *MonitorService) Create(ctx context.Context, userID int64, in CreateMonitorInput) (*domain.Monitor, error) {
	if err := validateMonitorInput(in.Name, in.URL, in.IntervalSeconds, in.TimeoutSeconds); err != nil {
		return nil, err
	}
	if in.Type == "" {
		in.Type = domain.MonitorTypeHTTP
	}
	if in.Type != domain.MonitorTypeHTTP && in.Type != domain.MonitorTypeTCP {
		return nil, ErrInvalidMonitorType
	}
	if in.ExpectedStatusCode == 0 {
		in.ExpectedStatusCode = 200
	}

	m := &domain.Monitor{
		UserID:             userID,
		Name:               in.Name,
		URL:                in.URL,
		Type:               in.Type,
		IntervalSeconds:    in.IntervalSeconds,
		TimeoutSeconds:     in.TimeoutSeconds,
		ExpectedStatusCode: in.ExpectedStatusCode,
		Active:             true,
	}

	if err := s.monitors.Create(ctx, m); err != nil {
		return nil, fmt.Errorf("service: creating monitor: %w", err)
	}
	return m, nil
}

// List returns only the requesting user's own monitors. Admins do not get
// an implicit "see everything" here — that's a deliberate choice, not an
// oversight: a dedicated admin-only endpoint (added when user management
// lands) is a clearer, more auditable place for cross-user visibility than
// quietly widening this one. Keeping List's behavior identical for every
// role means there's exactly one code path to reason about, not two.
func (s *MonitorService) List(ctx context.Context, userID int64) ([]*domain.Monitor, error) {
	monitors, err := s.monitors.ListByUser(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("service: listing monitors: %w", err)
	}
	return monitors, nil
}

// Get enforces ownership: a viewer/operator can only fetch their own
// monitors; an admin can fetch any. This check happens here, in the
// service layer, rather than in SQL — SQL could do it too (WHERE id = $1
// AND user_id = $2), but expressing it as a Go conditional keeps the "who
// is allowed to see what" policy in one readable place instead of smeared
// across every query in the repository.
func (s *MonitorService) Get(ctx context.Context, requesterID int64, requesterRole domain.Role, monitorID int64) (*domain.Monitor, error) {
	return s.authorizedMonitor(ctx, requesterID, requesterRole, monitorID)
}

// MonitorStats bundles the two SQL-computed aggregates for a single
// monitor over a time window. Kept as one struct rather than two separate
// method calls at the handler level so the handler makes one service call,
// not two — the service internally decides these two numbers belong
// together.
type MonitorStats struct {
	UptimePercent    float64
	AvgResponseTime  time.Duration
	WindowSince      time.Time
}

// Checks returns recent check history for a monitor, ownership-checked
// exactly like Get. "Recent history" and "the monitor itself" share the
// same access rule — if you can't view the monitor, you can't view its
// check history either — so this deliberately re-fetches and re-checks
// ownership via GetMonitorAuthorized rather than trusting a caller to have
// already validated access on a previous, separate call.
func (s *MonitorService) Checks(ctx context.Context, requesterID int64, requesterRole domain.Role, monitorID int64, since time.Time, limit int) ([]*domain.Check, error) {
	if _, err := s.authorizedMonitor(ctx, requesterID, requesterRole, monitorID); err != nil {
		return nil, err
	}
	checks, err := s.checks.ListByMonitor(ctx, monitorID, since, limit)
	if err != nil {
		return nil, fmt.Errorf("service: listing checks: %w", err)
	}
	return checks, nil
}

// Stats returns uptime % and average response time for a monitor over a
// given window, ownership-checked.
func (s *MonitorService) Stats(ctx context.Context, requesterID int64, requesterRole domain.Role, monitorID int64, since time.Time) (*MonitorStats, error) {
	if _, err := s.authorizedMonitor(ctx, requesterID, requesterRole, monitorID); err != nil {
		return nil, err
	}

	uptime, err := s.checks.UptimeSince(ctx, monitorID, since)
	if err != nil {
		return nil, fmt.Errorf("service: computing uptime: %w", err)
	}
	avgResponse, err := s.checks.AvgResponseTimeSince(ctx, monitorID, since)
	if err != nil {
		return nil, fmt.Errorf("service: computing average response time: %w", err)
	}

	return &MonitorStats{UptimePercent: uptime * 100, AvgResponseTime: avgResponse, WindowSince: since}, nil
}

// Incidents returns incident history for a monitor, ownership-checked.
func (s *MonitorService) Incidents(ctx context.Context, requesterID int64, requesterRole domain.Role, monitorID int64, limit int) ([]*domain.Incident, error) {
	if _, err := s.authorizedMonitor(ctx, requesterID, requesterRole, monitorID); err != nil {
		return nil, err
	}
	incidents, err := s.incidents.ListByMonitor(ctx, monitorID, limit)
	if err != nil {
		return nil, fmt.Errorf("service: listing incidents: %w", err)
	}
	return incidents, nil
}

// authorizedMonitor is the shared "load the monitor and check ownership"
// step used by Get, Checks, Stats, and Incidents — factored out once these
// four methods all needed the identical pattern, rather than copy-pasting
// the same two lines four times.
func (s *MonitorService) authorizedMonitor(ctx context.Context, requesterID int64, requesterRole domain.Role, monitorID int64) (*domain.Monitor, error) {
	m, err := s.monitors.GetByID(ctx, monitorID)
	if err != nil {
		return nil, err
	}
	if err := authorizeOwnership(m.UserID, requesterID, requesterRole); err != nil {
		return nil, err
	}
	return m, nil
}

func (s *MonitorService) Update(ctx context.Context, requesterID int64, requesterRole domain.Role, monitorID int64, in UpdateMonitorInput) (*domain.Monitor, error) {
	m, err := s.monitors.GetByID(ctx, monitorID)
	if err != nil {
		return nil, err
	}
	if err := authorizeOwnership(m.UserID, requesterID, requesterRole); err != nil {
		return nil, err
	}
	if err := validateMonitorInput(in.Name, in.URL, in.IntervalSeconds, in.TimeoutSeconds); err != nil {
		return nil, err
	}

	m.Name = in.Name
	m.URL = in.URL
	if in.Type != "" {
		m.Type = in.Type
	}
	m.IntervalSeconds = in.IntervalSeconds
	m.TimeoutSeconds = in.TimeoutSeconds
	if in.ExpectedStatusCode != 0 {
		m.ExpectedStatusCode = in.ExpectedStatusCode
	}
	m.Active = in.Active

	if err := s.monitors.Update(ctx, m); err != nil {
		return nil, fmt.Errorf("service: updating monitor: %w", err)
	}
	return m, nil
}

func (s *MonitorService) Delete(ctx context.Context, requesterID int64, requesterRole domain.Role, monitorID int64) error {
	m, err := s.monitors.GetByID(ctx, monitorID)
	if err != nil {
		return err
	}
	if err := authorizeOwnership(m.UserID, requesterID, requesterRole); err != nil {
		return err
	}
	if err := s.monitors.Delete(ctx, monitorID); err != nil {
		return fmt.Errorf("service: deleting monitor: %w", err)
	}
	return nil
}

// authorizeOwnership is the one place the "who can touch this monitor"
// rule is expressed: the owner, or an admin. Centralizing it means Get,
// Update, and Delete can never drift into inconsistent authorization
// behavior from each other.
func authorizeOwnership(ownerID, requesterID int64, requesterRole domain.Role) error {
	if requesterRole == domain.RoleAdmin {
		return nil
	}
	if ownerID != requesterID {
		return domain.ErrForbidden
	}
	return nil
}

func validateMonitorInput(name, rawURL string, intervalSeconds, timeoutSeconds int) error {
	if err := validation.ValidateName(name); err != nil {
		return err
	}
	if err := validation.ValidateURL(rawURL); err != nil {
		return err
	}
	if err := validation.ValidateInterval(intervalSeconds); err != nil {
		return err
	}
	if err := validation.ValidateTimeout(timeoutSeconds, intervalSeconds); err != nil {
		return err
	}
	return nil
}
