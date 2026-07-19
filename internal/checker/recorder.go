package checker

import (
	"context"
	"log/slog"

	"github.com/yourname/sitepulse/internal/domain"
)

// ResultRecorder is what happens to a completed check. Defining this as an
// interface — rather than having the worker pool import a database package
// directly — is the same dependency-inversion principle used everywhere
// else in this project: the concurrency machinery (WorkerPool, scheduler)
// shouldn't need to know or care whether results end up in Postgres, a log
// line, a metrics system, or all three. Phase 5 adds a Postgres-backed
// implementation of this same interface; nothing in this package changes
// when that happens.
type ResultRecorder interface {
	Record(ctx context.Context, check *domain.Check)
}

// LoggingRecorder is the Phase 4 default: every completed check becomes a
// structured log line. This is a legitimate, complete implementation on
// its own (not just a placeholder) — it's genuinely useful for local
// development and debugging, where watching checks flow through in real
// time is often more useful than querying a database. Phase 5's
// Postgres-backed recorder will typically be used alongside this one, not
// instead of it — see RecordAll below.
type LoggingRecorder struct {
	log *slog.Logger
}

func NewLoggingRecorder(log *slog.Logger) *LoggingRecorder {
	return &LoggingRecorder{log: log}
}

func (r *LoggingRecorder) Record(ctx context.Context, check *domain.Check) {
	attrs := []any{
		"monitor_id", check.MonitorID,
		"status", check.Status,
		"status_code", check.StatusCode,
		"response_time_ms", check.ResponseTime.Milliseconds(),
	}
	if check.Error != "" {
		attrs = append(attrs, "error", check.Error)
	}

	if check.Status == domain.CheckStatusDown {
		r.log.Warn("check result", attrs...)
	} else {
		r.log.Info("check result", attrs...)
	}
}

// PersistingRecorder writes every check result via a domain.CheckRepository.
// Note the dependency here is on the *interface* defined in domain, not on
// postgres.CheckRepository directly — this package still has zero
// knowledge that Postgres exists. main.go is the only place that wires the
// concrete Postgres implementation in, same pattern as every other layer
// in this project.
type PersistingRecorder struct {
	checks domain.CheckRepository
	log    *slog.Logger
}

func NewPersistingRecorder(checks domain.CheckRepository, log *slog.Logger) *PersistingRecorder {
	return &PersistingRecorder{checks: checks, log: log}
}

// Record deliberately swallows the persistence error rather than
// propagating it — ResultRecorder.Record has no error return (see the
// interface doc comment), specifically so a database hiccup on one check
// can never block or crash the scheduler's main loop, which has no
// business knowing or caring how results get stored. We do still log the
// failure at Error level so it's visible and investigable, just not fatal.
func (r *PersistingRecorder) Record(ctx context.Context, check *domain.Check) {
	if err := r.checks.Create(ctx, check); err != nil {
		r.log.Error(
			"failed to persist check result",
			"monitor_id", check.MonitorID,
			"error", err,
		)
	}
}

// MultiRecorder fans a single check result out to several recorders — e.g.
// log it AND persist it AND (later) evaluate it for alerting, without any
// one of those needing to know the others exist. Each recorder's
// Record is expected to handle its own errors internally (log them, etc.)
// rather than returning one, precisely so that one recorder's failure
// (say, a transient DB hiccup) can never block or skip the others.
type MultiRecorder struct {
	recorders []ResultRecorder
}

func NewMultiRecorder(recorders ...ResultRecorder) *MultiRecorder {
	return &MultiRecorder{recorders: recorders}
}

func (r *MultiRecorder) Record(ctx context.Context, check *domain.Check) {
	for _, rec := range r.recorders {
		rec.Record(ctx, check)
	}
}
