package service

import (
	"context"
	"errors"
	"log/slog"

	"github.com/yourname/sitepulse/internal/domain"
	"github.com/yourname/sitepulse/internal/notify"
)

// IncidentService turns individual check results into incident lifecycle
// events. It has no exported "open" or "resolve" methods to call
// directly — instead, its Record method has the exact signature
// checker.ResultRecorder expects (Record(ctx, *domain.Check)), so main.go
// plugs an *IncidentService directly into checker.NewMultiRecorder
// alongside the logging and persisting recorders. This works via Go's
// structural typing without service ever importing the checker package —
// the two packages don't know about each other, only about the shared
// method shape.
//
// The business rule itself is simple and deliberately conservative:
//   - A check comes back "down," and no incident is currently open for
//     that monitor → open one.
//   - A check comes back "up" or "slow," and an incident IS open →
//     resolve it. ("slow" resolves an incident — a sluggish-but-reachable
//     response means the outage is over, even if performance isn't
//     perfect; that's a monitoring nuance distinct from "still down.")
//   - Anything else (down while already open; up/slow while nothing's
//     open) is a no-op — the repository layer's unique constraint is the
//     actual backstop against double-opening, so this code doesn't need
//     to be defensively clever about races, just correct in the common
//     case.
type IncidentService struct {
	incidents domain.IncidentRepository
	monitors  domain.MonitorRepository
	notifier  notify.Notifier
	log       *slog.Logger
}

func NewIncidentService(incidents domain.IncidentRepository, monitors domain.MonitorRepository, notifier notify.Notifier, log *slog.Logger) *IncidentService {
	return &IncidentService{incidents: incidents, monitors: monitors, notifier: notifier, log: log}
}

func (s *IncidentService) Record(ctx context.Context, check *domain.Check) {
	if check.Status == domain.CheckStatusDown {
		s.handleDown(ctx, check)
		return
	}
	s.handleUpOrSlow(ctx, check)
}

func (s *IncidentService) handleDown(ctx context.Context, check *domain.Check) {
	_, err := s.incidents.GetOpenByMonitor(ctx, check.MonitorID)
	if err == nil {
		return // an incident is already open — this down check is part of the same ongoing outage
	}
	if !errors.Is(err, domain.ErrNotFound) {
		s.log.Error("incident service: failed to check for open incident", "monitor_id", check.MonitorID, "error", err)
		return
	}

	incident := &domain.Incident{
		MonitorID: check.MonitorID,
		Status:    domain.IncidentStatusOpen,
		StartedAt: check.CheckedAt,
		Cause:     check.Error,
	}
	if err := s.incidents.Create(ctx, incident); err != nil {
		s.log.Error("incident service: failed to open incident", "monitor_id", check.MonitorID, "error", err)
		return
	}
	s.log.Warn("incident opened", "monitor_id", check.MonitorID, "cause", check.Error)

	// Notification happens after the incident is durably recorded, not
	// before — if Create had failed, there'd be nothing to notify about
	// yet (and the next down check will simply retry opening it). Fetching
	// the monitor here (rather than requiring the caller to pass one in)
	// keeps IncidentService's Record signature identical to every other
	// ResultRecorder, at the cost of one extra lookup per incident open —
	// a rare event compared to the check volume, so the trade-off is fine.
	monitor, err := s.monitors.GetByID(ctx, check.MonitorID)
	if err != nil {
		s.log.Error("incident service: failed to load monitor for notification", "monitor_id", check.MonitorID, "error", err)
		return
	}
	s.notifier.Notify(ctx, notify.NewIncidentOpenedEvent(monitor, incident))
}

func (s *IncidentService) handleUpOrSlow(ctx context.Context, check *domain.Check) {
	open, err := s.incidents.GetOpenByMonitor(ctx, check.MonitorID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return // nothing open — nothing to resolve, the common case
		}
		s.log.Error("incident service: failed to check for open incident", "monitor_id", check.MonitorID, "error", err)
		return
	}

	if err := s.incidents.Resolve(ctx, open.ID, check.CheckedAt); err != nil {
		s.log.Error("incident service: failed to resolve incident", "monitor_id", check.MonitorID, "error", err)
		return
	}
	duration := check.CheckedAt.Sub(open.StartedAt)
	s.log.Info("incident resolved", "monitor_id", check.MonitorID, "incident_id", open.ID, "duration", duration)

	monitor, err := s.monitors.GetByID(ctx, check.MonitorID)
	if err != nil {
		s.log.Error("incident service: failed to load monitor for notification", "monitor_id", check.MonitorID, "error", err)
		return
	}
	open.Status = domain.IncidentStatusResolved
	s.notifier.Notify(ctx, notify.NewIncidentResolvedEvent(monitor, open, duration.String()))
}
