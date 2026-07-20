package handler

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/yourname/sitepulse/internal/domain"
	appmw "github.com/yourname/sitepulse/internal/middleware"
	"github.com/yourname/sitepulse/internal/service"
)

type UserHandler struct {
	users *service.UserService
	log   *slog.Logger
}

func NewUserHandler(users *service.UserService, log *slog.Logger) *UserHandler {
	return &UserHandler{users: users, log: log}
}

type userResponse struct {
	ID            int64  `json:"id"`
	Email         string `json:"email"`
	Role          string `json:"role"`
	EmailVerified bool   `json:"email_verified"`
	CreatedAt     string `json:"created_at"`
}

func toUserResponse(u *domain.User) userResponse {
	return userResponse{
		ID:            u.ID,
		Email:         u.Email,
		Role:          string(u.Role),
		EmailVerified: u.EmailVerified,
		CreatedAt:     u.CreatedAt.Format(timeFormat),
	}
}

const timeFormat = "2006-01-02T15:04:05Z07:00"

// Me returns the caller's own profile — open to any authenticated role,
// no admin check. This is what lets a client show "logged in as
// you@example.com (operator)" without needing a dedicated admin endpoint
// just to know who you are.
func (h *UserHandler) Me(w http.ResponseWriter, r *http.Request) {
	claims, err := appmw.ClaimsFromContext(r.Context())
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	u, err := h.users.Profile(r.Context(), claims.UserID)
	if err != nil {
		h.handleError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toUserResponse(u))
}

// List and Get are mounted behind RequireRole(admin) at the router level
// (see server.go) — the ErrNotAdmin check inside UserService is a second,
// independent layer behind that, not a substitute for it.
func (h *UserHandler) List(w http.ResponseWriter, r *http.Request) {
	claims, err := appmw.ClaimsFromContext(r.Context())
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	users, err := h.users.List(r.Context(), claims.Role)
	if err != nil {
		h.handleError(w, err)
		return
	}
	resp := make([]userResponse, 0, len(users))
	for _, u := range users {
		resp = append(resp, toUserResponse(u))
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *UserHandler) Get(w http.ResponseWriter, r *http.Request) {
	claims, err := appmw.ClaimsFromContext(r.Context())
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	id, err := parseIDParam(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid user id")
		return
	}
	u, err := h.users.Get(r.Context(), claims.Role, id)
	if err != nil {
		h.handleError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toUserResponse(u))
}

type updateRoleRequest struct {
	Role string `json:"role"`
}

func (h *UserHandler) UpdateRole(w http.ResponseWriter, r *http.Request) {
	claims, err := appmw.ClaimsFromContext(r.Context())
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	id, err := parseIDParam(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid user id")
		return
	}
	var req updateRoleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	u, err := h.users.UpdateRole(r.Context(), claims.UserID, claims.Role, id, domain.Role(req.Role))
	if err != nil {
		h.handleError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toUserResponse(u))
}

// handleError follows the same "never leak internals" caution used
// elsewhere. Here it's slightly different from MonitorHandler's
// ownership-based reasoning: an admin-only listing has no non-admin-visible
// resource to hide the existence of, so ErrNotAdmin maps to a plain 403 —
// there's no equivalent "could this leak whether the resource exists"
// concern the way there was with per-user monitor ownership.
func (h *UserHandler) handleError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, service.ErrNotAdmin):
		writeError(w, http.StatusForbidden, "admin role required")
	case errors.Is(err, domain.ErrNotFound):
		writeError(w, http.StatusNotFound, "user not found")
	case errors.Is(err, service.ErrInvalidRole):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, service.ErrCannotDemoteSelf):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, service.ErrLastAdmin):
		writeError(w, http.StatusConflict, err.Error())
	default:
		h.log.Error("user handler internal error", "error", err)
		writeError(w, http.StatusInternalServerError, "something went wrong")
	}
}
