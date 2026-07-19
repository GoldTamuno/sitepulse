package checker

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/yourname/sitepulse/internal/domain"
)

// fakeCheckRepo is a minimal in-memory domain.CheckRepository, used only
// to verify PersistingRecorder calls Create correctly — it doesn't need to
// implement the aggregate queries realistically since those aren't
// exercised by these tests.
type fakeCheckRepo struct {
	mu       sync.Mutex
	created  []*domain.Check
	failNext bool
}

func (r *fakeCheckRepo) Create(ctx context.Context, c *domain.Check) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failNext {
		r.failNext = false
		return errors.New("simulated database failure")
	}
	r.created = append(r.created, c)
	return nil
}

func (r *fakeCheckRepo) ListByMonitor(ctx context.Context, monitorID int64, since time.Time, limit int) ([]*domain.Check, error) {
	return nil, nil
}
func (r *fakeCheckRepo) UptimeSince(ctx context.Context, monitorID int64, since time.Time) (float64, error) {
	return 0, nil
}
func (r *fakeCheckRepo) AvgResponseTimeSince(ctx context.Context, monitorID int64, since time.Time) (time.Duration, error) {
	return 0, nil
}

func (r *fakeCheckRepo) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.created)
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func TestPersistingRecorder_RecordsSuccessfully(t *testing.T) {
	repo := &fakeCheckRepo{}
	rec := NewPersistingRecorder(repo, testLogger())

	check := &domain.Check{MonitorID: 1, Status: domain.CheckStatusUp, StatusCode: 200}
	rec.Record(context.Background(), check)

	if repo.count() != 1 {
		t.Fatalf("expected 1 check persisted, got %d", repo.count())
	}
}

func TestPersistingRecorder_SwallowsRepositoryErrors(t *testing.T) {
	// This is the behavior that matters most: a database failure on one
	// check must not panic, must not propagate an error the scheduler
	// would have to handle, and must not stop subsequent checks from
	// being recorded.
	repo := &fakeCheckRepo{failNext: true}
	rec := NewPersistingRecorder(repo, testLogger())

	rec.Record(context.Background(), &domain.Check{MonitorID: 1, Status: domain.CheckStatusUp})
	if repo.count() != 0 {
		t.Fatalf("expected the failed write to not be counted, got %d", repo.count())
	}

	// The next call should succeed normally — one failure doesn't wedge
	// the recorder.
	rec.Record(context.Background(), &domain.Check{MonitorID: 1, Status: domain.CheckStatusUp})
	if repo.count() != 1 {
		t.Fatalf("expected the recorder to recover after one failure, got count %d", repo.count())
	}
}

func TestMultiRecorder_FansOutToAllRecorders(t *testing.T) {
	repoA := &fakeCheckRepo{}
	repoB := &fakeCheckRepo{}
	recA := NewPersistingRecorder(repoA, testLogger())
	recB := NewPersistingRecorder(repoB, testLogger())

	multi := NewMultiRecorder(recA, recB)
	multi.Record(context.Background(), &domain.Check{MonitorID: 1, Status: domain.CheckStatusUp})

	if repoA.count() != 1 || repoB.count() != 1 {
		t.Fatalf("expected both underlying recorders to receive the check, got repoA=%d repoB=%d", repoA.count(), repoB.count())
	}
}

func TestMultiRecorder_OneFailureDoesNotBlockOthers(t *testing.T) {
	failing := &fakeCheckRepo{failNext: true}
	working := &fakeCheckRepo{}
	multi := NewMultiRecorder(
		NewPersistingRecorder(failing, testLogger()),
		NewPersistingRecorder(working, testLogger()),
	)

	multi.Record(context.Background(), &domain.Check{MonitorID: 1, Status: domain.CheckStatusUp})

	if working.count() != 1 {
		t.Fatalf("expected the working recorder to still receive the check despite the other failing, got %d", working.count())
	}
}
