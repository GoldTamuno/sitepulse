package handler

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/yourname/sitepulse/internal/security"
	"github.com/yourname/sitepulse/internal/service"
	"github.com/yourname/sitepulse/internal/validation"
)

const refreshCookieName = "sitepulse_refresh_token"

type AuthHandler struct {
	auth   *service.AuthService
	log    *slog.Logger
	secure bool // false only in local dev over http://
}

func NewAuthHandler(auth *service.AuthService, log *slog.Logger, secureCookies bool) *AuthHandler {
	return &AuthHandler{auth: auth, log: log, secure: secureCookies}
}

type registerRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type authResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int    `json:"expires_in"` // seconds, matches OAuth2 convention clients already expect
	User        struct {
		ID    int64  `json:"id"`
		Email string `json:"email"`
		Role  string `json:"role"`
	} `json:"user"`
}

func (h *AuthHandler) Register(w http.ResponseWriter, r *http.Request) {
	var req registerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	user, err := h.auth.Register(r.Context(), req.Email, req.Password)
	if err != nil {
		h.handleAuthError(w, err)
		return
	}

	// Registration deliberately does NOT auto-login in this version: email
	// verification (flagged in the schema via email_verified) is planned,
	// and auto-issuing tokens before verification exists would need
	// revisiting once that lands. For now, direct the client to log in.
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]any{
		"id":    user.ID,
		"email": user.Email,
		"role":  user.Role,
	})
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := validation.ValidateEmail(req.Email); err != nil {
		writeError(w, http.StatusBadRequest, "invalid email or password")
		return
	}

	user, tokens, err := h.auth.Login(r.Context(), req.Email, req.Password)
	if err != nil {
		h.handleAuthError(w, err)
		return
	}

	h.setRefreshCookie(w, tokens.RefreshToken, tokens.RefreshExpiresAt)
	h.writeAuthResponse(w, user.ID, user.Email, string(user.Role), tokens.AccessToken)
}

func (h *AuthHandler) Refresh(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie(refreshCookieName)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "missing refresh token")
		return
	}

	tokens, err := h.auth.Refresh(r.Context(), cookie.Value)
	if err != nil {
		// On reuse detection specifically, proactively clear the cookie —
		// the token the client is holding is dead and dangerous to retry.
		if errors.Is(err, service.ErrTokenReuse) {
			h.log.Warn("refresh token reuse detected", "remote_addr", r.RemoteAddr)
			h.clearRefreshCookie(w)
		}
		h.handleAuthError(w, err)
		return
	}

	h.setRefreshCookie(w, tokens.RefreshToken, tokens.RefreshExpiresAt)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"access_token": tokens.AccessToken,
		// The access token's TTL is a fixed constant, not derived from the
		// refresh token's expiry — these are two different lifetimes and
		// conflating them was the bug caught in manual testing above.
		"expires_in": int(security.AccessTokenTTL.Seconds()),
	})
}

func (h *AuthHandler) Logout(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie(refreshCookieName)
	if err == nil {
		if err := h.auth.Logout(r.Context(), cookie.Value); err != nil {
			h.log.Error("logout: revoking token family failed", "error", err)
			// Still clear the cookie client-side even if the server-side
			// revoke had an error — we don't want to leave the client
			// thinking it's logged in when it isn't, and we've logged the
			// failure for investigation.
		}
	}
	h.clearRefreshCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

func (h *AuthHandler) setRefreshCookie(w http.ResponseWriter, rawToken string, expiresAt time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     refreshCookieName,
		Value:    rawToken,
		Path:     "/api/v1/auth", // scoped narrowly — this cookie has no business being sent to /api/v1/monitors etc.
		HttpOnly: true,           // inaccessible to JS — the core XSS mitigation for this token
		Secure:   h.secure,       // false only for local http:// dev
		SameSite: http.SameSiteStrictMode,
		Expires:  expiresAt,
	})
}

func (h *AuthHandler) clearRefreshCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     refreshCookieName,
		Value:    "",
		Path:     "/api/v1/auth",
		HttpOnly: true,
		Secure:   h.secure,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
	})
}

func (h *AuthHandler) writeAuthResponse(w http.ResponseWriter, id int64, email, role, accessToken string) {
	resp := authResponse{
		AccessToken: accessToken,
		ExpiresIn:   int(security.AccessTokenTTL.Seconds()),
	}
	resp.User.ID = id
	resp.User.Email = email
	resp.User.Role = role

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// handleAuthError maps internal errors to safe, generic client responses.
// This is the "never expose internal implementation details" requirement
// in practice: service.ErrInvalidCredentials becomes a flat 401 with no
// hint whether the email existed; anything unexpected becomes a bare 500
// with the real error only reaching the server log, never the client.
func (h *AuthHandler) handleAuthError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, service.ErrEmailTaken):
		writeError(w, http.StatusConflict, "email already registered")
	case errors.Is(err, service.ErrInvalidCredentials):
		writeError(w, http.StatusUnauthorized, "invalid email or password")
	case errors.Is(err, service.ErrTokenInvalid), errors.Is(err, service.ErrTokenReuse):
		writeError(w, http.StatusUnauthorized, "session expired, please log in again")
	case errors.Is(err, validation.ErrInvalidEmail),
		errors.Is(err, validation.ErrPasswordTooShort),
		errors.Is(err, validation.ErrPasswordTooWeak),
		errors.Is(err, validation.ErrPasswordTooLong):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		h.log.Error("auth handler internal error", "error", err)
		writeError(w, http.StatusInternalServerError, "something went wrong")
	}
}

func writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": message})
}
