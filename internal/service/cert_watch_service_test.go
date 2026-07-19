package service

import (
	"context"
	"testing"
	"time"

	"github.com/yourname/sitepulse/internal/domain"
	"github.com/yourname/sitepulse/internal/notify"
)

// countingNotifier counts how many times Notify was called, so tests can
// assert on de-duplication (exactly once, not once per check).
type countingNotifier struct {
	calls int
}

func (n *countingNotifier) Notify(ctx context.Context, event notify.Event) {
	n.calls++
}

func newTestCertWatchService(t *testing.T, warnDays int) (*CertWatchService, *countingNotifier) {
	t.Helper()
	monitorRepo := newFakeMonitorRepo()
	monitorRepo.byID[1] = &domain.Monitor{ID: 1, Name: "Test Monitor", URL: "https://example.com"}

	counter := &countingNotifier{}
	svc := NewCertWatchService(monitorRepo, counter, warnDays, testIncidentLogger())
	return svc, counter
}

func TestCertWatchService_WarnsWhenWithinThreshold(t *testing.T) {
	svc, counter := newTestCertWatchService(t, 14)
	expiresAt := time.Now().Add(5 * 24 * time.Hour) // 5 days out, threshold is 14

	svc.Record(context.Background(), &domain.Check{MonitorID: 1, TLSExpiresAt: &expiresAt})

	if counter.calls != 1 {
		t.Fatalf("expected exactly 1 notification, got %d", counter.calls)
	}
}

func TestCertWatchService_DoesNotWarnWhenFarFromExpiry(t *testing.T) {
	svc, counter := newTestCertWatchService(t, 14)
	expiresAt := time.Now().Add(60 * 24 * time.Hour) // 60 days out, well beyond the 14-day threshold

	svc.Record(context.Background(), &domain.Check{MonitorID: 1, TLSExpiresAt: &expiresAt})

	if counter.calls != 0 {
		t.Fatalf("expected no notification for a cert nowhere near expiry, got %d calls", counter.calls)
	}
}

func TestCertWatchService_DoesNotWarnForAlreadyExpiredCert(t *testing.T) {
	// An already-expired cert is causing (or will cause) TLS handshake
	// failures, which surface as a "down" check and an incident via
	// IncidentService — CertWatchService's job is the advance warning
	// only, not re-reporting a failure another part of the system already
	// owns.
	svc, counter := newTestCertWatchService(t, 14)
	expiresAt := time.Now().Add(-1 * time.Hour)

	svc.Record(context.Background(), &domain.Check{MonitorID: 1, TLSExpiresAt: &expiresAt})

	if counter.calls != 0 {
		t.Fatalf("expected no notification for an already-expired cert, got %d calls", counter.calls)
	}
}

func TestCertWatchService_DoesNotWarnForHTTPMonitors(t *testing.T) {
	svc, counter := newTestCertWatchService(t, 14)

	svc.Record(context.Background(), &domain.Check{MonitorID: 1, TLSExpiresAt: nil})

	if counter.calls != 0 {
		t.Fatalf("expected no notification when TLSExpiresAt is nil, got %d calls", counter.calls)
	}
}

func TestCertWatchService_DoesNotSpamRepeatedWarnings(t *testing.T) {
	svc, counter := newTestCertWatchService(t, 14)
	expiresAt := time.Now().Add(5 * 24 * time.Hour)

	// Simulate the monitor being checked many times in a row while still
	// within the warning window — this is the realistic scenario (checks
	// every 30s for days) that de-duplication exists to prevent.
	for i := 0; i < 20; i++ {
		svc.Record(context.Background(), &domain.Check{MonitorID: 1, TLSExpiresAt: &expiresAt})
	}

	if counter.calls != 1 {
		t.Fatalf("expected exactly 1 notification despite 20 checks within the cooldown window, got %d", counter.calls)
	}
}
