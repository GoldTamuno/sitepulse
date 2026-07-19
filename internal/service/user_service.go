package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/yourname/sitepulse/internal/domain"
)

var (
	// ErrNotAdmin is a defense-in-depth check inside the service layer
	// itself, even though every route calling these methods is already
	// wrapped in middleware.RequireRole(domain.RoleAdmin). This mirrors
	// the project's general security posture: never trust a single layer
	// alone. If a future code path ever calls UserService directly
	// (a background job, a future internal tool) without going through
	// the HTTP middleware chain, this check is what still stops a
	// non-admin caller — the middleware isn't the only thing standing
	// between "authenticated" and "authorized to manage every account."
	ErrNotAdmin = errors.New("service: admin role required")

	ErrInvalidRole      = errors.New("service: role must be admin, operator, or viewer")
	ErrCannotDemoteSelf = errors.New("service: cannot change your own role")
	ErrLastAdmin        = errors.New("service: cannot demote the last remaining admin")
)

type UserService struct {
	users domain.UserRepository
}

func NewUserService(users domain.UserRepository) *UserService {
	return &UserService{users: users}
}

func (s *UserService) List(ctx context.Context, requesterRole domain.Role) ([]*domain.User, error) {
	if requesterRole != domain.RoleAdmin {
		return nil, ErrNotAdmin
	}
	users, err := s.users.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("service: listing users: %w", err)
	}
	return users, nil
}

func (s *UserService) Get(ctx context.Context, requesterRole domain.Role, targetID int64) (*domain.User, error) {
	if requesterRole != domain.RoleAdmin {
		return nil, ErrNotAdmin
	}
	u, err := s.users.GetByID(ctx, targetID)
	if err != nil {
		return nil, err // domain.ErrNotFound propagates as-is
	}
	return u, nil
}

// Profile is the self-service equivalent of Get: any authenticated user
// can view their own record, no admin role required, no ErrNotAdmin check
// — this is deliberately a completely separate method rather than Get
// with a bypass flag, so the "you can only see yourself" and "an admin can
// see anyone" rules are never accidentally tangled together in one
// conditional.
func (s *UserService) Profile(ctx context.Context, userID int64) (*domain.User, error) {
	u, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	return u, nil
}

// UpdateRole changes a target user's role, enforced by three separate
// rules stacked in order — each one is a distinct real-world safety
// concern, checked as its own explicit step rather than folded into one
// dense conditional, specifically so each has its own clear error and each
// is independently testable:
//
//  1. Only an admin can change anyone's role at all.
//  2. An admin can't change their OWN role — self-demotion (accidental or
//     malicious, e.g. a compromised admin session trying to lock out other
//     admins by demoting itself to hide activity) is blocked outright.
//     Role changes to your own account must come from a different admin.
//  3. The system can never be left with zero admins. If the target is
//     currently the only admin and the new role isn't admin, the change is
//     rejected — otherwise a well-meaning "let me clean up unused admin
//     accounts" action could accidentally lock everyone out of user
//     management permanently, with no way to self-recover short of a
//     direct database edit.
func (s *UserService) UpdateRole(ctx context.Context, requesterID int64, requesterRole domain.Role, targetID int64, newRole domain.Role) (*domain.User, error) {
	if requesterRole != domain.RoleAdmin {
		return nil, ErrNotAdmin
	}
	if !isValidRole(newRole) {
		return nil, ErrInvalidRole
	}
	if requesterID == targetID {
		return nil, ErrCannotDemoteSelf
	}

	target, err := s.users.GetByID(ctx, targetID)
	if err != nil {
		return nil, err
	}

	if target.Role == domain.RoleAdmin && newRole != domain.RoleAdmin {
		adminCount, err := s.users.CountByRole(ctx, domain.RoleAdmin)
		if err != nil {
			return nil, fmt.Errorf("service: counting admins: %w", err)
		}
		if adminCount <= 1 {
			return nil, ErrLastAdmin
		}
	}

	if err := s.users.UpdateRole(ctx, targetID, newRole); err != nil {
		return nil, fmt.Errorf("service: updating role: %w", err)
	}
	target.Role = newRole
	return target, nil
}

func isValidRole(r domain.Role) bool {
	switch r {
	case domain.RoleAdmin, domain.RoleOperator, domain.RoleViewer:
		return true
	default:
		return false
	}
}
