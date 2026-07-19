package service

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/yourname/sitepulse/internal/domain"
	"github.com/yourname/sitepulse/internal/notify"
)

// CertWatchService is another checker.ResultRecorder-shaped type (same
// structural-typing trick as IncidentService — this package never imports
// checker). It inspects every check's TLSExpiresAt and warns once a
// certificate is within a configurable window of expiring.
//
// The de-duplication is the interesting part: without it, a monitor
// checked every 30 seconds with a cert expiring in 10 days would trigger
// roughly 28,800 identical warnings before the threshold passes — useless
// noise that would drown out every other notification. lastWarned tracks,
// per monitor, when we last sent this warning, and warnCooldown bounds how
// often we're willing to repeat it (once, then not again for a day) —
// mirroring the same "in-memory state owned by one type" pattern as
// scheduler.nextRun, protected here with a mutex because — unlike
// scheduler, which is only ever touched by its own single goroutine —
// CertWatchService.Record can be invoked concurrently by multiple worker
// goroutines from the pool.
type CertWatchService struct {
	monitors      domain.MonitorRepository
	notifier      notify.Notifier
	log           *slog.Logger
	warnThreshold time.Duration
	warnCooldown  time.Duration

	mu         sync.Mutex
	lastWarned map[int64]time.Time
}

func NewCertWatchService(monitors domain.MonitorRepository, notifier notify.Notifier, warnDays int, log *slog.Logger) *CertWatchService {
	return &CertWatchService{
		monitors:      monitors,
		notifier:      notifier,
		log:           log,
		warnThreshold: time.Duration(warnDays) * 24 * time.Hour,
		warnCooldown:  24 * time.Hour,
		lastWarned:    make(map[int64]time.Time),
	}
}

func (s *CertWatchService) Record(ctx context.Context, check *domain.Check) {
	if check.TLSExpiresAt == nil {
		return // http:// monitor, or the check never got far enough to see a cert
	}

	remaining := time.Until(*check.TLSExpiresAt)
	if remaining < 0 || remaining > s.warnThreshold {
		// Already expired, or not close enough yet. An already-expired
		// cert will also be causing TLS handshake failures, which surface
		// as a "down" check and an incident through IncidentService — this
		// service's job is specifically the advance warning, not the
		// failure itself.
		return
	}

	if !s.shouldWarn(check.MonitorID) {
		return
	}

	monitor, err := s.monitors.GetByID(ctx, check.MonitorID)
	if err != nil {
		s.log.Error("cert watch: failed to load monitor for notification", "monitor_id", check.MonitorID, "error", err)
		return
	}

	s.notifier.Notify(ctx, notify.NewCertExpiringSoonEvent(monitor, remaining.Round(time.Hour).String()))
}

func (s *CertWatchService) shouldWarn(monitorID int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	last, seen := s.lastWarned[monitorID]
	if seen && time.Since(last) < s.warnCooldown {
		return false
	}
	s.lastWarned[monitorID] = time.Now()
	return true
}
