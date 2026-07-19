// Package checker performs the actual work of checking whether a monitored
// endpoint is healthy. This package knows nothing about scheduling ("when"
// to check) or persistence ("what happens to the result") — it does exactly
// one job: given a monitor, produce a domain.Check describing its current
// state. That single responsibility is what makes it independently
// testable with httptest.Server and reusable regardless of how checks get
// triggered.
package checker

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/yourname/sitepulse/internal/domain"
)

// maxRetries and retryBackoff govern how RunCheck handles transient
// failures — a DNS blip, a dropped TCP connection, a connection refused
// during a brief restart on the target's end. These are retried a small,
// bounded number of times before being recorded as a real outage.
//
// Deliberately NOT retried: a successful HTTP response with the wrong
// status code (e.g. expected 200, got 500). That's not a transient
// network problem — it's the target telling us something is actually
// wrong, and retrying it would just delay detecting a real outage for no
// benefit. The distinction between "the network misbehaved" and "the
// target responded and told us it's unhealthy" is the whole reason this
// isn't a single blanket retry-everything policy.
const (
	maxRetries    = 2
	retryBackoff  = 500 * time.Millisecond
)

// httpClient is shared across all checks rather than constructed per-call.
// http.Client is safe for concurrent use by multiple goroutines, and
// reusing one client lets Go's transport pool and reuse TCP connections
// across checks to the same host — constructing a fresh client (and
// therefore a fresh connection pool) per check would throw that away for
// no benefit.
var httpClient = &http.Client{
	// No client-level Timeout is set here deliberately — each check
	// supplies its own context.WithTimeout derived from the monitor's
	// configured TimeoutSeconds, which gives per-monitor control. A
	// single shared client-level timeout would force every monitor to
	// share the same timeout regardless of its own configuration.
	CheckRedirect: nil, // use default: follow up to 10 redirects, which is what a real "is this site up" check should do
}

// RunCheck performs one health check attempt against a monitor, including
// retries for transient failures, and always returns a fully-populated
// domain.Check — never an error. This is intentional: a failed check IS a
// valid, meaningful result (the target is down), not an exceptional
// condition. Forcing callers to handle a Go `error` return here would
// blur the line between "the check ran and found a problem" (expected,
// common) and "the checker itself is broken" (which would be a bug, and
// isn't a case this function needs to handle — a malformed URL is caught
// by validation before a Monitor ever reaches this function).
func RunCheck(ctx context.Context, m *domain.Monitor, slowThreshold time.Duration) *domain.Check {
	var lastErr error
	var statusCode int
	var elapsed time.Duration
	var tlsExpiresAt *time.Time

	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-time.After(retryBackoff):
			case <-ctx.Done():
				return downCheck(m, ctx.Err(), 0)
			}
		}

		start := time.Now()
		result, err := attemptRequest(ctx, m)
		elapsed = time.Since(start)
		lastErr = err
		if result != nil {
			statusCode = result.statusCode
			tlsExpiresAt = result.tlsExpiresAt
		}

		if err == nil {
			// Got a response at all (regardless of status code) — that's
			// not a transient network failure, so stop retrying here even
			// if the status code doesn't match what's expected.
			break
		}
		if !isTransient(err) {
			break
		}
		// else: transient network-level error, loop again (up to maxRetries)
	}

	if lastErr != nil {
		return downCheck(m, lastErr, elapsed)
	}

	if statusCode != m.ExpectedStatusCode {
		return &domain.Check{
			MonitorID:    m.ID,
			Status:       domain.CheckStatusDown,
			StatusCode:   statusCode,
			ResponseTime: elapsed,
			Error:        "unexpected status code",
			TLSExpiresAt: tlsExpiresAt,
			CheckedAt:    time.Now(),
		}
	}

	status := domain.CheckStatusUp
	if elapsed > slowThreshold {
		status = domain.CheckStatusSlow
	}

	return &domain.Check{
		MonitorID:    m.ID,
		Status:       status,
		StatusCode:   statusCode,
		ResponseTime: elapsed,
		TLSExpiresAt: tlsExpiresAt,
		CheckedAt:    time.Now(),
	}
}

// requestResult carries back everything attemptRequest learned from a
// single HTTP round trip, beyond just the status code.
type requestResult struct {
	statusCode   int
	tlsExpiresAt *time.Time
}

// attemptRequest performs a single HTTP GET, bounded by the monitor's own
// configured timeout layered onto the parent context — so a check is
// bounded both by "the scheduler/worker pool is shutting down" (parent
// ctx) and "this specific monitor said 10 seconds max" (the timeout added
// here), whichever fires first.
func attemptRequest(ctx context.Context, m *domain.Monitor) (*requestResult, error) {
	checkCtx, cancel := context.WithTimeout(ctx, time.Duration(m.TimeoutSeconds)*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(checkCtx, http.MethodGet, m.URL, nil)
	if err != nil {
		return nil, err
	}
	// Identify ourselves. Some services block requests with no/blank
	// User-Agent, and it's simply good practice for a monitoring bot to
	// be identifiable in the target's access logs.
	req.Header.Set("User-Agent", "SitePulse-Monitor/1.0")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	result := &requestResult{statusCode: resp.StatusCode}

	// resp.TLS is populated by the net/http client whenever the request
	// went over TLS (i.e., an https:// URL) and is nil for plain http://.
	// PeerCertificates[0] is the leaf (server) certificate — the one whose
	// expiry actually matters for "is this site's cert about to expire,"
	// as opposed to intermediate/root CA certs further down the chain.
	if resp.TLS != nil && len(resp.TLS.PeerCertificates) > 0 {
		expiry := resp.TLS.PeerCertificates[0].NotAfter
		result.tlsExpiresAt = &expiry
	}

	return result, nil
}

// isTransient decides whether an error is worth retrying. context
// deadline/cancellation is explicitly NOT retried — if the timeout or
// parent context already expired, retrying just burns more time against a
// budget that's already spent. Everything else from the net/http client
// (connection refused, DNS failure, connection reset) is treated as
// possibly transient and worth one more attempt.
func isTransient(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return false
	}
	return true
}

func downCheck(m *domain.Monitor, err error, elapsed time.Duration) *domain.Check {
	return &domain.Check{
		MonitorID:    m.ID,
		Status:       domain.CheckStatusDown,
		StatusCode:   0,
		ResponseTime: elapsed,
		Error:        err.Error(),
		CheckedAt:    time.Now(),
	}
}
