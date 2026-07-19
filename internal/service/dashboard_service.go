package service

import (
	"context"
	"fmt"
	"time"

	"github.com/yourname/sitepulse/internal/domain"
)

// DashboardSummary is the top-level "how is everything doing" view — the
// numbers a landing dashboard shows before a user drills into any single
// monitor.
type DashboardSummary struct {
	TotalMonitors      int
	HealthyCount       int // most recent check was "up" or "slow"
	UnhealthyCount     int // most recent check was "down"
	PendingCount       int // no checks recorded yet (monitor just created)
	AvgUptimePercent   float64
	AvgResponseTime    time.Duration
	WindowSince        time.Time
}

type DashboardService struct {
	monitors domain.MonitorRepository
	checks   domain.CheckRepository
}

func NewDashboardService(monitors domain.MonitorRepository, checks domain.CheckRepository) *DashboardService {
	return &DashboardService{monitors: monitors, checks: checks}
}

// Summary computes the dashboard view for one user's monitors over a given
// window (e.g. the last 24 hours).
//
// A known, deliberate simplification worth being upfront about: this
// issues 3 queries per monitor (latest check, uptime, avg response time),
// so for a user with N monitors this is O(N) database round trips, not a
// single batched query. At the scale this project runs at (a portfolio
// project's realistic monitor count — tens, not tens of thousands), that's
// perfectly fine and the code stays simple and easy to follow. A
// production system carrying thousands of monitors per user would want
// this collapsed into one or two batched SQL queries (e.g. a single query
// with a LATERAL join or window functions per monitor) — a legitimate
// future optimization, not something worth the added query complexity
// until it's actually needed.
//
// AvgUptimePercent and AvgResponseTime are unweighted means across
// monitors (each monitor counts equally regardless of how many checks it
// has accumulated) — simple and predictable, though a monitor checked
// every 10 seconds and one checked every hour contribute equally to the
// average. Worth knowing, not necessarily worth fixing at this scale.
func (s *DashboardService) Summary(ctx context.Context, userID int64, window time.Duration) (*DashboardSummary, error) {
	monitors, err := s.monitors.ListByUser(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("service: listing monitors for dashboard: %w", err)
	}

	since := time.Now().Add(-window)
	summary := &DashboardSummary{TotalMonitors: len(monitors), WindowSince: since}

	var uptimeSum, responseSum float64
	var statsCount int

	for _, m := range monitors {
		latest, err := s.checks.ListByMonitor(ctx, m.ID, time.Time{}, 1)
		if err != nil {
			return nil, fmt.Errorf("service: fetching latest check for monitor %d: %w", m.ID, err)
		}
		switch {
		case len(latest) == 0:
			summary.PendingCount++
		case latest[0].Status == domain.CheckStatusDown:
			summary.UnhealthyCount++
		default:
			summary.HealthyCount++
		}

		uptime, err := s.checks.UptimeSince(ctx, m.ID, since)
		if err != nil {
			return nil, fmt.Errorf("service: computing uptime for monitor %d: %w", m.ID, err)
		}
		avgResponse, err := s.checks.AvgResponseTimeSince(ctx, m.ID, since)
		if err != nil {
			return nil, fmt.Errorf("service: computing avg response time for monitor %d: %w", m.ID, err)
		}

		// Only monitors with at least one check in the window contribute
		// to the averages — a brand-new monitor with zero checks
		// shouldn't drag the average uptime toward 0%, since it hasn't
		// actually failed, it just hasn't run yet.
		if len(latest) > 0 {
			uptimeSum += uptime
			responseSum += float64(avgResponse)
			statsCount++
		}
	}

	if statsCount > 0 {
		summary.AvgUptimePercent = (uptimeSum / float64(statsCount)) * 100
		summary.AvgResponseTime = time.Duration(responseSum / float64(statsCount))
	}

	return summary, nil
}
