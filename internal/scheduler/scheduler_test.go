package scheduler

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/yourname/sitepulse/internal/checker"
	"github.com/yourname/sitepulse/internal/domain"
)

// fakeMonitorRepo is a minimal domain.MonitorRepository for scheduler
// tests — only ListActive is actually exercised, the rest exist purely to
// satisfy the interface.
type fakeMonitorRepo struct {
	monitors []*domain.Monitor
}

func (r *fakeMonitorRepo) Create(ctx context.Context, m *domain.Monitor) error { return nil }
func (r *fakeMonitorRepo) GetByID(ctx context.Context, id int64) (*domain.Monitor, error) {
	return nil, domain.ErrNotFound
}

func (r *fakeMonitorRepo) ListByUser(ctx context.Context, userID int64) ([]*domain.Monitor, error) {
	return nil, nil
}

func (r *fakeMonitorRepo) ListActive(ctx context.Context) ([]*domain.Monitor, error) {
	return r.monitors, nil
}
func (r *fakeMonitorRepo) Update(ctx context.Context, m *domain.Monitor) error { return nil }
func (r *fakeMonitorRepo) Delete(ctx context.Context, id int64) error          { return nil }

// capturingRecorder collects every check it receives, safely under
// concurrent access — the scheduler's result consumer runs in its own
// goroutine, so a test recorder needs its own synchronization just like a
// real one would (e.g. a DB connection pool is already safe for this;
// a plain slice is not).
type capturingRecorder struct {
	mu     sync.Mutex
	checks []*domain.Check
}

func (r *capturingRecorder) Record(ctx context.Context, check *domain.Check) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.checks = append(r.checks, check)
}

func (r *capturingRecorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.checks)
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func TestScheduler_IsDue(t *testing.T) {
	s := New(time.Second, &fakeMonitorRepo{}, nil, nil, testLogger())
	now := time.Now()

	t.Run("a monitor never seen before is due immediately", func(t *testing.T) {
		if !s.isDue(1, now) {
			t.Error("expected an unseen monitor to be due")
		}
	})

	t.Run("a monitor checked recently is not due yet", func(t *testing.T) {
		s.nextRun[2] = now.Add(1 * time.Minute)
		if s.isDue(2, now) {
			t.Error("expected a monitor with a future nextRun to not be due")
		}
	})

	t.Run("a monitor whose nextRun has passed is due", func(t *testing.T) {
		s.nextRun[3] = now.Add(-1 * time.Minute)
		if !s.isDue(3, now) {
			t.Error("expected a monitor with a past nextRun to be due")
		}
	})

	t.Run("a monitor due exactly now is due (boundary is inclusive)", func(t *testing.T) {
		s.nextRun[4] = now
		if !s.isDue(4, now) {
			t.Error("expected a monitor due exactly at 'now' to be due")
		}
	})
}

// TestScheduler_RunAndShutdown is an integration test across scheduler,
// checker.WorkerPool, and a real (local, in-process) HTTP server — this is
// the test that actually exercises the goroutine coordination: does a
// monitor get checked, does the result reach the recorder, and does
// cancelling the context lead to a clean, bounded-time shutdown rather
// than a hang or a panic.
func TestScheduler_RunAndShutdown(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	repo := &fakeMonitorRepo{
		monitors: []*domain.Monitor{
			{ID: 1, URL: srv.URL, ExpectedStatusCode: http.StatusOK, TimeoutSeconds: 2, IntervalSeconds: 3600, Active: true},
		},
	}
	recorder := &capturingRecorder{}
	pool := checker.NewWorkerPool(2, 2*time.Second, testLogger())
	sched := New(20*time.Millisecond, repo, pool, recorder, testLogger())

	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan struct{})
	go func() {
		defer close(runDone)
		sched.Run(ctx)
	}()

	// Poll briefly for at least one check to land, rather than a fixed
	// sleep — this is both faster on a fast machine and less flaky on a
	// slow one than guessing a fixed duration.
	deadline := time.Now().Add(2 * time.Second)
	for recorder.count() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if recorder.count() == 0 {
		t.Fatal("expected at least one check result to be recorded within 2s, got none")
	}

	cancel()

	select {
	case <-runDone:
		// clean shutdown
	case <-time.After(3 * time.Second):
		t.Fatal("scheduler did not shut down within 3s of context cancellation — possible goroutine leak or deadlock")
	}
}
