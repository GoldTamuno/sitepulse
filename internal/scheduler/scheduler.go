// Package scheduler answers exactly one question on a fixed cadence:
// "which monitors are due for a check right now?" — and submits those to
// a checker.WorkerPool. It deliberately knows nothing about how a check is
// performed (checker's job) or what happens to results (ResultRecorder's
// job); its only responsibility is timing.
package scheduler

import (
	"context"
	"log/slog"
	"time"

	"github.com/yourname/sitepulse/internal/checker"
	"github.com/yourname/sitepulse/internal/domain"
)

// Scheduler wakes up every `tick` and asks: for each active monitor, has
// enough time passed since it was last checked to run it again? Each
// monitor has its own IntervalSeconds, so this can't just be "check
// everything every tick" — a monitor configured for a 5-minute interval
// shouldn't get hit every 5 seconds just because the scheduler's own tick
// is 5 seconds.
type Scheduler struct {
	tick     time.Duration
	monitors domain.MonitorRepository
	pool     *checker.WorkerPool
	recorder checker.ResultRecorder
	log      *slog.Logger

	// nextRun tracks, per monitor ID, the earliest time it's due to run
	// again. This is intentionally in-memory rather than a database
	// column: it's derived, ephemeral scheduling state, not a fact worth
	// persisting — if the process restarts, every monitor simply becomes
	// immediately due again (a monitor missing on an in-memory map
	// defaults to "due now," see isDue below), which is a perfectly
	// reasonable behavior after a restart. This map is only ever read and
	// written from the single goroutine running Run's loop, so it needs
	// no mutex — a common and deliberate simplification for
	// single-goroutine-owned state, worth calling out explicitly since
	// "no mutex" is easy to mistake for an oversight rather than a choice.
	nextRun map[int64]time.Time
}

func New(tick time.Duration, monitors domain.MonitorRepository, pool *checker.WorkerPool, recorder checker.ResultRecorder, log *slog.Logger) *Scheduler {
	return &Scheduler{
		tick:     tick,
		monitors: monitors,
		pool:     pool,
		recorder: recorder,
		log:      log,
		nextRun:  make(map[int64]time.Time),
	}
}

// Run is the scheduler's main loop. It's designed to be launched with
// `go sched.Run(ctx)` and blocks until ctx is cancelled, at which point it
// performs an orderly shutdown of everything downstream (worker pool,
// result consumer) before returning — so a caller can `select` on some
// "scheduler fully stopped" signal (see cmd/api/main.go) rather than just
// firing a goroutine and hoping it cleans up in time.
func (s *Scheduler) Run(ctx context.Context) {
	s.pool.Start(ctx)

	// The result-consumer runs in its own goroutine so that draining
	// results never blocks the scheduling loop below — scheduling
	// (deciding what's due) and recording (persisting/logging what
	// happened) are two different concerns running concurrently,
	// connected only by the results channel.
	consumerDone := make(chan struct{})
	go func() {
		defer close(consumerDone)
		for check := range s.pool.Results() {
			s.recorder.Record(ctx, check)
		}
	}()

	ticker := time.NewTicker(s.tick)
	defer ticker.Stop()

	s.log.Info("scheduler started", "tick_interval", s.tick)

	for {
		select {
		case <-ctx.Done():
			s.log.Info("scheduler stopping, draining in-flight checks...")
			s.pool.Shutdown() // closes results, which ends the consumer goroutine's range loop
			<-consumerDone    // wait for every already-completed result to actually be recorded
			s.log.Info("scheduler stopped cleanly")
			return

		case now := <-ticker.C:
			s.runDueChecks(ctx, now)
		}
	}
}

func (s *Scheduler) runDueChecks(ctx context.Context, now time.Time) {
	monitors, err := s.monitors.ListActive(ctx)
	if err != nil {
		s.log.Error("scheduler: failed to list active monitors", "error", err)
		return
	}

	dueCount := 0
	for _, m := range monitors {
		if !s.isDue(m.ID, now) {
			continue
		}
		s.pool.Submit(ctx, checker.Job{Monitor: m})
		s.nextRun[m.ID] = now.Add(time.Duration(m.IntervalSeconds) * time.Second)
		dueCount++
	}

	if dueCount > 0 {
		s.log.Debug("submitted due checks", "count", dueCount, "total_active", len(monitors))
	}
}

func (s *Scheduler) isDue(monitorID int64, now time.Time) bool {
	next, seen := s.nextRun[monitorID]
	if !seen {
		return true // never checked before (or scheduler just started) — due immediately
	}
	return !now.Before(next)
}
