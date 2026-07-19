package service

import (
	"context"
	"testing"
	"time"

	"github.com/yourname/sitepulse/internal/domain"
)

func TestDashboardService_Summary(t *testing.T) {
	monitorRepo := newFakeMonitorRepo()
	checkRepo := newFakeCheckRepo()
	svc := NewDashboardService(monitorRepo, checkRepo)
	ctx := context.Background()

	monitorSvc := NewMonitorService(monitorRepo, checkRepo, newFakeIncidentRepo())

	// Monitor 1: healthy, one recent "up" check.
	m1, err := monitorSvc.Create(ctx, 1, validCreateInput())
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	mustCreateCheck(t, checkRepo, m1.ID, domain.CheckStatusUp, 100*time.Millisecond, time.Now())

	// Monitor 2: unhealthy, most recent check is "down" (even though an
	// earlier check was "up" — only the LATEST check should determine
	// health, not history).
	m2, err := monitorSvc.Create(ctx, 1, validCreateInput())
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	mustCreateCheck(t, checkRepo, m2.ID, domain.CheckStatusUp, 100*time.Millisecond, time.Now().Add(-time.Hour))
	mustCreateCheck(t, checkRepo, m2.ID, domain.CheckStatusDown, 0, time.Now())

	// Monitor 3: pending, no checks recorded yet at all.
	if _, err := monitorSvc.Create(ctx, 1, validCreateInput()); err != nil {
		t.Fatalf("setup: %v", err)
	}

	summary, err := svc.Summary(ctx, 1, 24*time.Hour)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if summary.TotalMonitors != 3 {
		t.Errorf("expected TotalMonitors 3, got %d", summary.TotalMonitors)
	}
	if summary.HealthyCount != 1 {
		t.Errorf("expected HealthyCount 1, got %d", summary.HealthyCount)
	}
	if summary.UnhealthyCount != 1 {
		t.Errorf("expected UnhealthyCount 1 (monitor 2's LATEST check is down), got %d", summary.UnhealthyCount)
	}
	if summary.PendingCount != 1 {
		t.Errorf("expected PendingCount 1 (monitor 3 has zero checks), got %d", summary.PendingCount)
	}
}

func TestDashboardService_SummaryWithNoMonitors(t *testing.T) {
	// A brand-new user with zero monitors should get a clean zero-value
	// summary, not an error or a divide-by-zero panic.
	monitorRepo := newFakeMonitorRepo()
	checkRepo := newFakeCheckRepo()
	svc := NewDashboardService(monitorRepo, checkRepo)

	summary, err := svc.Summary(context.Background(), 999, 24*time.Hour)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if summary.TotalMonitors != 0 || summary.AvgUptimePercent != 0 {
		t.Errorf("expected a zero-value summary for a user with no monitors, got %+v", summary)
	}
}

func mustCreateCheck(t *testing.T, repo *fakeCheckRepo, monitorID int64, status domain.CheckStatus, responseTime time.Duration, checkedAt time.Time) {
	t.Helper()
	c := &domain.Check{MonitorID: monitorID, Status: status, ResponseTime: responseTime, CheckedAt: checkedAt}
	if status == domain.CheckStatusUp || status == domain.CheckStatusSlow {
		c.StatusCode = 200
	}
	if err := repo.Create(context.Background(), c); err != nil {
		t.Fatalf("failed to seed check: %v", err)
	}
}
