package service

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/yourname/sitepulse/internal/domain"
)

// fakeUserRepo is a minimal in-memory implementation of
// domain.UserRepository. This is the entire payoff of designing AuthService
// against an interface instead of a concrete *postgres.UserRepository: this
// fake needs no Docker, no network, no migrations — go test just runs it in
// milliseconds, in-process.
type fakeUserRepo struct {
	mu     sync.Mutex
	byID   map[int64]*domain.User
	nextID int64
}

func newFakeUserRepo() *fakeUserRepo {
	return &fakeUserRepo{byID: make(map[int64]*domain.User)}
}

func (r *fakeUserRepo) Create(ctx context.Context, u *domain.User) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, existing := range r.byID {
		if existing.Email == u.Email {
			return domain.ErrConflict
		}
	}

	r.nextID++
	u.ID = r.nextID
	stored := *u
	r.byID[u.ID] = &stored
	return nil
}

func (r *fakeUserRepo) GetByEmail(ctx context.Context, email string) (*domain.User, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, u := range r.byID {
		if u.Email == email {
			cp := *u
			return &cp, nil
		}
	}
	return nil, domain.ErrNotFound
}

func (r *fakeUserRepo) GetByID(ctx context.Context, id int64) (*domain.User, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	u, ok := r.byID[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	cp := *u
	return &cp, nil
}

func (r *fakeUserRepo) List(ctx context.Context) ([]*domain.User, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	users := make([]*domain.User, 0, len(r.byID))
	for _, u := range r.byID {
		cp := *u
		users = append(users, &cp)
	}
	return users, nil
}

func (r *fakeUserRepo) UpdateRole(ctx context.Context, id int64, role domain.Role) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	u, ok := r.byID[id]
	if !ok {
		return domain.ErrNotFound
	}
	u.Role = role
	return nil
}

func (r *fakeUserRepo) CountByRole(ctx context.Context, role domain.Role) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	count := 0
	for _, u := range r.byID {
		if u.Role == role {
			count++
		}
	}
	return count, nil
}

// fakeRefreshTokenRepo is the in-memory equivalent for
// domain.RefreshTokenRepository — this is what lets us test rotation and
// reuse detection without a real refresh_tokens table.
type fakeRefreshTokenRepo struct {
	mu     sync.Mutex
	byID   map[int64]*domain.RefreshToken
	nextID int64
}

func newFakeRefreshTokenRepo() *fakeRefreshTokenRepo {
	return &fakeRefreshTokenRepo{byID: make(map[int64]*domain.RefreshToken)}
}

func (r *fakeRefreshTokenRepo) Create(ctx context.Context, t *domain.RefreshToken) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.nextID++
	t.ID = r.nextID
	stored := *t
	r.byID[t.ID] = &stored
	return nil
}

func (r *fakeRefreshTokenRepo) GetByHash(ctx context.Context, tokenHash string) (*domain.RefreshToken, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, t := range r.byID {
		if t.TokenHash == tokenHash {
			cp := *t
			return &cp, nil
		}
	}
	return nil, domain.ErrNotFound
}

func (r *fakeRefreshTokenRepo) RevokeFamily(ctx context.Context, familyID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, t := range r.byID {
		if t.FamilyID == familyID {
			t.Revoked = true
		}
	}
	return nil
}

func (r *fakeRefreshTokenRepo) Revoke(ctx context.Context, id int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if t, ok := r.byID[id]; ok {
		t.Revoked = true
	}
	return nil
}

func (r *fakeRefreshTokenRepo) DeleteExpired(ctx context.Context) (int64, error) {
	return 0, nil // not exercised by AuthService tests
}

// fakeMonitorRepo is the in-memory equivalent for domain.MonitorRepository,
// used by MonitorService's tests.
type fakeMonitorRepo struct {
	mu     sync.Mutex
	byID   map[int64]*domain.Monitor
	nextID int64
}

func newFakeMonitorRepo() *fakeMonitorRepo {
	return &fakeMonitorRepo{byID: make(map[int64]*domain.Monitor)}
}

func (r *fakeMonitorRepo) Create(ctx context.Context, m *domain.Monitor) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.nextID++
	m.ID = r.nextID
	stored := *m
	r.byID[m.ID] = &stored
	return nil
}

func (r *fakeMonitorRepo) GetByID(ctx context.Context, id int64) (*domain.Monitor, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	m, ok := r.byID[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	cp := *m
	return &cp, nil
}

func (r *fakeMonitorRepo) ListByUser(ctx context.Context, userID int64) ([]*domain.Monitor, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	var result []*domain.Monitor
	for _, m := range r.byID {
		if m.UserID == userID {
			cp := *m
			result = append(result, &cp)
		}
	}
	return result, nil
}

func (r *fakeMonitorRepo) ListActive(ctx context.Context) ([]*domain.Monitor, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	var result []*domain.Monitor
	for _, m := range r.byID {
		if m.Active {
			cp := *m
			result = append(result, &cp)
		}
	}
	return result, nil
}

func (r *fakeMonitorRepo) Update(ctx context.Context, m *domain.Monitor) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.byID[m.ID]; !ok {
		return domain.ErrNotFound
	}
	stored := *m
	r.byID[m.ID] = &stored
	return nil
}

func (r *fakeMonitorRepo) Delete(ctx context.Context, id int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.byID[id]; !ok {
		return domain.ErrNotFound
	}
	delete(r.byID, id)
	return nil
}

// fakeIncidentRepo is the in-memory equivalent for
// domain.IncidentRepository, used by IncidentService's tests.
type fakeIncidentRepo struct {
	mu     sync.Mutex
	byID   map[int64]*domain.Incident
	nextID int64
}

func newFakeIncidentRepo() *fakeIncidentRepo {
	return &fakeIncidentRepo{byID: make(map[int64]*domain.Incident)}
}

func (r *fakeIncidentRepo) Create(ctx context.Context, i *domain.Incident) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Mirror the real repository's behavior: reject (silently, as a
	// no-op) opening a second incident for a monitor that already has one
	// open — this is what lets the same test double exercise the same
	// "race is harmless" guarantee the real unique index provides.
	for _, existing := range r.byID {
		if existing.MonitorID == i.MonitorID && existing.Status == domain.IncidentStatusOpen {
			return nil
		}
	}

	r.nextID++
	i.ID = r.nextID
	stored := *i
	r.byID[i.ID] = &stored
	return nil
}

func (r *fakeIncidentRepo) GetOpenByMonitor(ctx context.Context, monitorID int64) (*domain.Incident, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, i := range r.byID {
		if i.MonitorID == monitorID && i.Status == domain.IncidentStatusOpen {
			cp := *i
			return &cp, nil
		}
	}
	return nil, domain.ErrNotFound
}

func (r *fakeIncidentRepo) Resolve(ctx context.Context, id int64, resolvedAt time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	i, ok := r.byID[id]
	if !ok {
		return domain.ErrNotFound
	}
	i.Status = domain.IncidentStatusResolved
	i.ResolvedAt = &resolvedAt
	return nil
}

func (r *fakeIncidentRepo) ListByMonitor(ctx context.Context, monitorID int64, limit int) ([]*domain.Incident, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	var result []*domain.Incident
	for _, i := range r.byID {
		if i.MonitorID == monitorID {
			cp := *i
			result = append(result, &cp)
		}
	}
	return result, nil
}

// fakeCheckRepo is the in-memory equivalent for domain.CheckRepository,
// used by MonitorService and DashboardService tests. UptimeSince and
// AvgResponseTimeSince do real (if simple) computation over the stored
// checks, rather than returning a hardcoded value — this is what lets
// tests actually verify the aggregation logic each service builds on top
// of, not just that a repository method was called.
type fakeCheckRepo struct {
	mu     sync.Mutex
	byID   map[int64]*domain.Check
	nextID int64
}

func newFakeCheckRepo() *fakeCheckRepo {
	return &fakeCheckRepo{byID: make(map[int64]*domain.Check)}
}

func (r *fakeCheckRepo) Create(ctx context.Context, c *domain.Check) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.nextID++
	c.ID = r.nextID
	stored := *c
	r.byID[c.ID] = &stored
	return nil
}

func (r *fakeCheckRepo) ListByMonitor(ctx context.Context, monitorID int64, since time.Time, limit int) ([]*domain.Check, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	var result []*domain.Check
	for _, c := range r.byID {
		if c.MonitorID == monitorID && !c.CheckedAt.Before(since) {
			cp := *c
			result = append(result, &cp)
		}
	}
	// Sort newest first, matching the real repository's ORDER BY checked_at DESC.
	sort.Slice(result, func(i, j int) bool { return result[i].CheckedAt.After(result[j].CheckedAt) })
	if limit > 0 && len(result) > limit {
		result = result[:limit]
	}
	return result, nil
}

func (r *fakeCheckRepo) UptimeSince(ctx context.Context, monitorID int64, since time.Time) (float64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	var total, up int
	for _, c := range r.byID {
		if c.MonitorID == monitorID && !c.CheckedAt.Before(since) {
			total++
			if c.Status != domain.CheckStatusDown {
				up++
			}
		}
	}
	if total == 0 {
		return 0, nil
	}
	return float64(up) / float64(total), nil
}

func (r *fakeCheckRepo) AvgResponseTimeSince(ctx context.Context, monitorID int64, since time.Time) (time.Duration, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	var total time.Duration
	var count int
	for _, c := range r.byID {
		if c.MonitorID == monitorID && !c.CheckedAt.Before(since) && c.Status != domain.CheckStatusDown {
			total += c.ResponseTime
			count++
		}
	}
	if count == 0 {
		return 0, nil
	}
	return total / time.Duration(count), nil
}
