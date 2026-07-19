package service

import (
	"context"
	"errors"
	"testing"

	"github.com/yourname/sitepulse/internal/domain"
)

// seedUser creates a user directly via the repo (bypassing AuthService's
// validation/hashing, which isn't relevant to these tests) and returns its
// assigned ID.
func seedUser(t *testing.T, repo *fakeUserRepo, email string, role domain.Role) int64 {
	t.Helper()
	u := &domain.User{Email: email, PasswordHash: "unused", Role: role}
	if err := repo.Create(context.Background(), u); err != nil {
		t.Fatalf("seeding user failed: %v", err)
	}
	return u.ID
}

func TestUserService_List_RequiresAdmin(t *testing.T) {
	repo := newFakeUserRepo()
	svc := NewUserService(repo)
	seedUser(t, repo, "a@example.com", domain.RoleViewer)

	_, err := svc.List(context.Background(), domain.RoleViewer)
	if !errors.Is(err, ErrNotAdmin) {
		t.Fatalf("expected ErrNotAdmin for a non-admin caller, got %v", err)
	}

	users, err := svc.List(context.Background(), domain.RoleAdmin)
	if err != nil {
		t.Fatalf("unexpected error for admin caller: %v", err)
	}
	if len(users) != 1 {
		t.Fatalf("expected 1 user, got %d", len(users))
	}
}

func TestUserService_Profile_NoAdminRequired(t *testing.T) {
	// Profile is the self-service lookup — any authenticated role, no
	// admin check, unlike Get.
	repo := newFakeUserRepo()
	svc := NewUserService(repo)
	id := seedUser(t, repo, "viewer@example.com", domain.RoleViewer)

	u, err := svc.Profile(context.Background(), id)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if u.Email != "viewer@example.com" {
		t.Errorf("expected email %q, got %q", "viewer@example.com", u.Email)
	}
}

func TestUserService_UpdateRole_RequiresAdmin(t *testing.T) {
	repo := newFakeUserRepo()
	svc := NewUserService(repo)
	adminID := seedUser(t, repo, "admin@example.com", domain.RoleAdmin)
	targetID := seedUser(t, repo, "target@example.com", domain.RoleViewer)

	_, err := svc.UpdateRole(context.Background(), targetID, domain.RoleViewer, targetID, domain.RoleOperator)
	if !errors.Is(err, ErrNotAdmin) {
		t.Fatalf("expected ErrNotAdmin for a non-admin caller, got %v", err)
	}

	// Sanity: the same call succeeds when the caller actually is an admin
	// acting on someone else.
	_, err = svc.UpdateRole(context.Background(), adminID, domain.RoleAdmin, targetID, domain.RoleOperator)
	if err != nil {
		t.Fatalf("unexpected error for a legitimate admin role change: %v", err)
	}
}

func TestUserService_UpdateRole_RejectsInvalidRole(t *testing.T) {
	repo := newFakeUserRepo()
	svc := NewUserService(repo)
	adminID := seedUser(t, repo, "admin@example.com", domain.RoleAdmin)
	targetID := seedUser(t, repo, "target@example.com", domain.RoleViewer)

	_, err := svc.UpdateRole(context.Background(), adminID, domain.RoleAdmin, targetID, domain.Role("superuser"))
	if !errors.Is(err, ErrInvalidRole) {
		t.Fatalf("expected ErrInvalidRole, got %v", err)
	}
}

func TestUserService_UpdateRole_CannotChangeOwnRole(t *testing.T) {
	repo := newFakeUserRepo()
	svc := NewUserService(repo)
	// Two admins, so this isn't also hitting the last-admin rule — this
	// test isolates self-demotion specifically.
	adminID := seedUser(t, repo, "admin@example.com", domain.RoleAdmin)
	seedUser(t, repo, "admin2@example.com", domain.RoleAdmin)

	_, err := svc.UpdateRole(context.Background(), adminID, domain.RoleAdmin, adminID, domain.RoleViewer)
	if !errors.Is(err, ErrCannotDemoteSelf) {
		t.Fatalf("expected ErrCannotDemoteSelf, got %v", err)
	}
}

func TestUserService_UpdateRole_CannotDemoteLastAdmin(t *testing.T) {
	repo := newFakeUserRepo()
	svc := NewUserService(repo)
	// A second admin account acts as the requester, demoting the ONLY
	// other admin — this isolates the last-admin rule from the
	// self-demotion rule (which would otherwise also fire if the sole
	// admin tried to demote themselves).
	requesterID := seedUser(t, repo, "requester@example.com", domain.RoleAdmin)
	soleOtherAdminID := seedUser(t, repo, "sole-admin@example.com", domain.RoleAdmin)

	// Demote the requester itself down to operator first, so
	// soleOtherAdminID becomes the ONLY admin row left in the table. The
	// requester still presents an admin role claim when calling UpdateRole
	// below (simulating an admin whose JWT hasn't expired yet, even though
	// their stored role changed) — modeling "someone with a still-valid
	// admin token tries to demote the only remaining admin."
	if err := repo.UpdateRole(context.Background(), requesterID, domain.RoleOperator); err != nil {
		t.Fatalf("test setup failed: %v", err)
	}

	_, err := svc.UpdateRole(context.Background(), requesterID, domain.RoleAdmin, soleOtherAdminID, domain.RoleViewer)
	if !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("expected ErrLastAdmin, got %v", err)
	}

	// Confirm the role genuinely did not change.
	target, _ := repo.GetByID(context.Background(), soleOtherAdminID)
	if target.Role != domain.RoleAdmin {
		t.Errorf("expected the last admin's role to remain unchanged, got %q", target.Role)
	}
}

func TestUserService_UpdateRole_AllowsDemotionWhenMultipleAdminsExist(t *testing.T) {
	repo := newFakeUserRepo()
	svc := NewUserService(repo)
	requesterID := seedUser(t, repo, "requester@example.com", domain.RoleAdmin)
	targetID := seedUser(t, repo, "target@example.com", domain.RoleAdmin)

	updated, err := svc.UpdateRole(context.Background(), requesterID, domain.RoleAdmin, targetID, domain.RoleOperator)
	if err != nil {
		t.Fatalf("expected demotion to succeed with 2 admins present, got error: %v", err)
	}
	if updated.Role != domain.RoleOperator {
		t.Errorf("expected updated role %q, got %q", domain.RoleOperator, updated.Role)
	}
}
