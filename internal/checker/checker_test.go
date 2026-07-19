package checker

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/yourname/sitepulse/internal/domain"
)

func testMonitor(url string, expectedStatus, timeoutSeconds int) *domain.Monitor {
	return &domain.Monitor{
		ID:                 1,
		URL:                url,
		ExpectedStatusCode: expectedStatus,
		TimeoutSeconds:     timeoutSeconds,
	}
}

func TestRunCheck_HealthyResponseIsUp(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	m := testMonitor(srv.URL, http.StatusOK, 5)
	check := RunCheck(context.Background(), m, 2*time.Second)

	if check.Status != domain.CheckStatusUp {
		t.Errorf("expected status %q, got %q (error: %s)", domain.CheckStatusUp, check.Status, check.Error)
	}
	if check.StatusCode != http.StatusOK {
		t.Errorf("expected status code 200, got %d", check.StatusCode)
	}
	if check.MonitorID != m.ID {
		t.Errorf("expected MonitorID %d, got %d", m.ID, check.MonitorID)
	}
}

func TestRunCheck_UnexpectedStatusCodeIsDown(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	m := testMonitor(srv.URL, http.StatusOK, 5) // expects 200, server returns 500
	check := RunCheck(context.Background(), m, 2*time.Second)

	if check.Status != domain.CheckStatusDown {
		t.Errorf("expected status %q, got %q", domain.CheckStatusDown, check.Status)
	}
	if check.StatusCode != http.StatusInternalServerError {
		t.Errorf("expected recorded status code 500, got %d", check.StatusCode)
	}
	if check.Error == "" {
		t.Error("expected a non-empty error explaining the status mismatch")
	}
}

func TestRunCheck_SlowResponseIsFlaggedSlow(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(50 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	m := testMonitor(srv.URL, http.StatusOK, 5)
	// slowThreshold shorter than the server's deliberate delay, but well
	// under the monitor's 5s timeout — this response should succeed AND
	// be flagged slow, which is a different thing from failing outright.
	check := RunCheck(context.Background(), m, 10*time.Millisecond)

	if check.Status != domain.CheckStatusSlow {
		t.Errorf("expected status %q, got %q", domain.CheckStatusSlow, check.Status)
	}
	if check.StatusCode != http.StatusOK {
		t.Errorf("a slow check should still record the real status code, got %d", check.StatusCode)
	}
}

func TestRunCheck_TimeoutIsDown(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// Monitor's own timeout (in whole seconds, per the domain type) can't
	// express 200ms directly, so we exercise the timeout path via the
	// parent context instead — this is exactly the "parent context expired
	// first" branch attemptRequest is built to respect.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	m := testMonitor(srv.URL, http.StatusOK, 5)
	check := RunCheck(ctx, m, 2*time.Second)

	if check.Status != domain.CheckStatusDown {
		t.Errorf("expected status %q on timeout, got %q", domain.CheckStatusDown, check.Status)
	}
	if check.Error == "" {
		t.Error("expected a non-empty error describing the timeout")
	}
}

func TestRunCheck_ConnectionRefusedIsDown(t *testing.T) {
	// Nothing is listening on this address — guaranteed connection refused,
	// no network flakiness risk since we never actually reach the network.
	m := testMonitor("http://127.0.0.1:1", http.StatusOK, 2)

	start := time.Now()
	check := RunCheck(context.Background(), m, 2*time.Second)
	elapsed := time.Since(start)

	if check.Status != domain.CheckStatusDown {
		t.Errorf("expected status %q, got %q", domain.CheckStatusDown, check.Status)
	}
	// Connection refused is treated as transient and retried (maxRetries=2
	// with a 500ms backoff between attempts) — confirm the retries
	// actually happened rather than failing instantly on the first
	// attempt, which is what makes this resilient to real transient blips.
	if elapsed < 2*retryBackoff {
		t.Errorf("expected retries to add up to at least %v of elapsed time, got %v — retry logic may not be running", 2*retryBackoff, elapsed)
	}
}

func TestRunCheck_CapturesTLSCertificateExpiry(t *testing.T) {
	// httptest.NewTLSServer generates a self-signed cert for the duration
	// of the test. We don't control its exact expiry date, but we can
	// assert that RunCheck actually populated the field with something in
	// the future — proving the TLS-capture code path runs at all, which is
	// what matters here (the exact expiry value is httptest's business,
	// not ours).
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// httptest's TLS server uses a self-signed cert our default http.Client
	// won't trust — swap in the test server's own client (which trusts it)
	// for the duration of this test, then restore the real one.
	original := httpClient
	httpClient = srv.Client()
	defer func() { httpClient = original }()

	m := testMonitor(srv.URL, http.StatusOK, 5)
	check := RunCheck(context.Background(), m, 2*time.Second)

	if check.Status != domain.CheckStatusUp {
		t.Fatalf("expected status %q, got %q (error: %s)", domain.CheckStatusUp, check.Status, check.Error)
	}
	if check.TLSExpiresAt == nil {
		t.Fatal("expected TLSExpiresAt to be populated for an https:// monitor, got nil")
	}
	if !check.TLSExpiresAt.After(time.Now()) {
		t.Errorf("expected TLSExpiresAt to be in the future, got %v", check.TLSExpiresAt)
	}
}

func TestRunCheck_HTTPMonitorHasNoTLSExpiry(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	m := testMonitor(srv.URL, http.StatusOK, 5) // srv.URL is http://, not https://
	check := RunCheck(context.Background(), m, 2*time.Second)

	if check.TLSExpiresAt != nil {
		t.Errorf("expected TLSExpiresAt to be nil for a plain http:// monitor, got %v", check.TLSExpiresAt)
	}
}

func TestRunCheck_DefaultsExpectedStatusHandledByCaller(t *testing.T) {
	// RunCheck itself does no defaulting of ExpectedStatusCode — that's
	// MonitorService's job at creation time (see monitor_service.go). A
	// monitor with ExpectedStatusCode left at zero would never match any
	// real HTTP status, and RunCheck correctly reports that as down rather
	// than silently guessing what the caller meant.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	m := testMonitor(srv.URL, 0, 5)
	check := RunCheck(context.Background(), m, 2*time.Second)

	if check.Status != domain.CheckStatusDown {
		t.Errorf("expected status %q for a zero-value ExpectedStatusCode, got %q", domain.CheckStatusDown, check.Status)
	}
}
