package service

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/yourname/sitepulse/internal/domain"
	"github.com/yourname/sitepulse/internal/notify"
)

// newTestIncidentService wires an IncidentService with a monitor
// pre-seeded at ID 1 (matching every test's check.MonitorID below) so the
// notification path — which requires loading the monitor — is fully
// exercised, not silently skipped. Uses a real notify.LoggingNotifier
// rather than a fake: it's harmless (just logs), genuinely exercises the
// Notifier interface call, and avoids needing yet another test double for
// something that has no meaningful state to assert on here.
func newTestIncidentService(t *testing.T) (*IncidentService, *fakeIncidentRepo) {
	t.Helper()
	incidentRepo := newFakeIncidentRepo()
	monitorRepo := newFakeMonitorRepo()
	monitorRepo.byID[1] = &domain.Monitor{ID: 1, Name: "Test Monitor", URL: "https://example.com"}

	svc := NewIncidentService(incidentRepo, monitorRepo, notify.NewLoggingNotifier(testIncidentLogger()), testIncidentLogger())
	return svc, incidentRepo
}

func TestIncidentService_OpensIncidentOnDown(t *testing.T) {
	svc, repo := newTestIncidentService(t)
	ctx := context.Background()

	svc.Record(ctx, &domain.Check{MonitorID: 1, Status: domain.CheckStatusDown, Error: "connection refused", CheckedAt: time.Now()})

	open, err := repo.GetOpenByMonitor(ctx, 1)
	if err != nil {
		t.Fatalf("expected an open incident, got error: %v", err)
	}
	if open.Cause != "connection refused" {
		t.Errorf("expected cause %q, got %q", "connection refused", open.Cause)
	}
}

func TestIncidentService_DoesNotDuplicateOpenIncidents(t *testing.T) {
	svc, repo := newTestIncidentService(t)
	ctx := context.Background()

	// Three consecutive down checks for the same outage — should result
	// in exactly ONE incident, not three.
	for i := 0; i < 3; i++ {
		svc.Record(ctx, &domain.Check{MonitorID: 1, Status: domain.CheckStatusDown, Error: "down", CheckedAt: time.Now()})
	}

	incidents, err := repo.ListByMonitor(ctx, 1, 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(incidents) != 1 {
		t.Fatalf("expected exactly 1 incident from 3 consecutive down checks, got %d", len(incidents))
	}
}

func TestIncidentService_ResolvesIncidentOnUp(t *testing.T) {
	svc, repo := newTestIncidentService(t)
	ctx := context.Background()

	svc.Record(ctx, &domain.Check{MonitorID: 1, Status: domain.CheckStatusDown, Error: "down", CheckedAt: time.Now()})
	svc.Record(ctx, &domain.Check{MonitorID: 1, Status: domain.CheckStatusUp, CheckedAt: time.Now()})

	_, err := repo.GetOpenByMonitor(ctx, 1)
	if err == nil {
		t.Fatal("expected no open incident after an up check resolved it")
	}

	incidents, _ := repo.ListByMonitor(ctx, 1, 10)
	if len(incidents) != 1 || incidents[0].Status != domain.IncidentStatusResolved {
		t.Fatalf("expected exactly 1 resolved incident, got %+v", incidents)
	}
}

func TestIncidentService_SlowAlsoResolvesAnIncident(t *testing.T) {
	// A "slow" check means the target is reachable again — the outage is
	// over, even if performance isn't great. This is a deliberate business
	// rule distinct from treating "slow" as a continuation of downtime.
	svc, repo := newTestIncidentService(t)
	ctx := context.Background()

	svc.Record(ctx, &domain.Check{MonitorID: 1, Status: domain.CheckStatusDown, CheckedAt: time.Now()})
	svc.Record(ctx, &domain.Check{MonitorID: 1, Status: domain.CheckStatusSlow, CheckedAt: time.Now()})

	if _, err := repo.GetOpenByMonitor(ctx, 1); err == nil {
		t.Fatal("expected a 'slow' check to resolve the open incident")
	}
}

func TestIncidentService_UpWithNoOpenIncidentIsANoOp(t *testing.T) {
	svc, repo := newTestIncidentService(t)
	ctx := context.Background()

	// No incident was ever opened — an up check should just do nothing,
	// not error or panic.
	svc.Record(ctx, &domain.Check{MonitorID: 1, Status: domain.CheckStatusUp, CheckedAt: time.Now()})

	incidents, _ := repo.ListByMonitor(ctx, 1, 10)
	if len(incidents) != 0 {
		t.Fatalf("expected no incidents to be created, got %d", len(incidents))
	}
}

func TestIncidentService_ReopensAfterANewOutage(t *testing.T) {
	// A full down -> up -> down cycle should produce two SEPARATE
	// incidents, not one incident being reused — each outage is its own
	// event with its own start time.
	svc, repo := newTestIncidentService(t)
	ctx := context.Background()

	svc.Record(ctx, &domain.Check{MonitorID: 1, Status: domain.CheckStatusDown, CheckedAt: time.Now()})
	svc.Record(ctx, &domain.Check{MonitorID: 1, Status: domain.CheckStatusUp, CheckedAt: time.Now()})
	svc.Record(ctx, &domain.Check{MonitorID: 1, Status: domain.CheckStatusDown, CheckedAt: time.Now()})

	incidents, _ := repo.ListByMonitor(ctx, 1, 10)
	if len(incidents) != 2 {
		t.Fatalf("expected 2 incidents across two separate outages, got %d", len(incidents))
	}

	open, err := repo.GetOpenByMonitor(ctx, 1)
	if err != nil {
		t.Fatalf("expected the second outage to have an open incident, got error: %v", err)
	}
	_ = open
}

func testIncidentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}
