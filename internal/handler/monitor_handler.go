package handler

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/yourname/sitepulse/internal/domain"
	appmw "github.com/yourname/sitepulse/internal/middleware"
	"github.com/yourname/sitepulse/internal/service"
	"github.com/yourname/sitepulse/internal/validation"
)

type MonitorHandler struct {
	monitors *service.MonitorService
	log      *slog.Logger
}

func NewMonitorHandler(monitors *service.MonitorService, log *slog.Logger) *MonitorHandler {
	return &MonitorHandler{monitors: monitors, log: log}
}

type monitorRequest struct {
	Name               string `json:"name"`
	URL                string `json:"url"`
	Type               string `json:"type"`
	IntervalSeconds    int    `json:"interval_seconds"`
	TimeoutSeconds     int    `json:"timeout_seconds"`
	ExpectedStatusCode int    `json:"expected_status_code"`
	Active             bool   `json:"active"`
}

type monitorResponse struct {
	ID                 int64  `json:"id"`
	Name               string `json:"name"`
	URL                string `json:"url"`
	Type               string `json:"type"`
	IntervalSeconds    int    `json:"interval_seconds"`
	TimeoutSeconds     int    `json:"timeout_seconds"`
	ExpectedStatusCode int    `json:"expected_status_code"`
	Active             bool   `json:"active"`
	CreatedAt          string `json:"created_at"`
	UpdatedAt          string `json:"updated_at"`
}

func toMonitorResponse(m *domain.Monitor) monitorResponse {
	return monitorResponse{
		ID:                 m.ID,
		Name:               m.Name,
		URL:                m.URL,
		Type:               string(m.Type),
		IntervalSeconds:    m.IntervalSeconds,
		TimeoutSeconds:     m.TimeoutSeconds,
		ExpectedStatusCode: m.ExpectedStatusCode,
		Active:             m.Active,
		CreatedAt:          m.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		UpdatedAt:          m.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}
}

func (h *MonitorHandler) Create(w http.ResponseWriter, r *http.Request) {
	claims, err := appmw.ClaimsFromContext(r.Context())
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	var req monitorRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	m, err := h.monitors.Create(r.Context(), claims.UserID, service.CreateMonitorInput{
		Name:               req.Name,
		URL:                req.URL,
		Type:               domain.MonitorType(req.Type),
		IntervalSeconds:    req.IntervalSeconds,
		TimeoutSeconds:     req.TimeoutSeconds,
		ExpectedStatusCode: req.ExpectedStatusCode,
	})
	if err != nil {
		h.handleError(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, toMonitorResponse(m))
}

func (h *MonitorHandler) List(w http.ResponseWriter, r *http.Request) {
	claims, err := appmw.ClaimsFromContext(r.Context())
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	monitors, err := h.monitors.List(r.Context(), claims.UserID)
	if err != nil {
		h.handleError(w, err)
		return
	}

	resp := make([]monitorResponse, 0, len(monitors))
	for _, m := range monitors {
		resp = append(resp, toMonitorResponse(m))
	}

	writeJSON(w, http.StatusOK, resp)
}

func (h *MonitorHandler) Get(w http.ResponseWriter, r *http.Request) {
	claims, err := appmw.ClaimsFromContext(r.Context())
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	id, err := parseIDParam(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid monitor id")
		return
	}

	m, err := h.monitors.Get(r.Context(), claims.UserID, claims.Role, id)
	if err != nil {
		h.handleError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, toMonitorResponse(m))
}

func (h *MonitorHandler) Update(w http.ResponseWriter, r *http.Request) {
	claims, err := appmw.ClaimsFromContext(r.Context())
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	id, err := parseIDParam(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid monitor id")
		return
	}

	var req monitorRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	m, err := h.monitors.Update(r.Context(), claims.UserID, claims.Role, id, service.UpdateMonitorInput{
		Name:               req.Name,
		URL:                req.URL,
		Type:               domain.MonitorType(req.Type),
		IntervalSeconds:    req.IntervalSeconds,
		TimeoutSeconds:     req.TimeoutSeconds,
		ExpectedStatusCode: req.ExpectedStatusCode,
		Active:             req.Active,
	})
	if err != nil {
		h.handleError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, toMonitorResponse(m))
}

func (h *MonitorHandler) Delete(w http.ResponseWriter, r *http.Request) {
	claims, err := appmw.ClaimsFromContext(r.Context())
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	id, err := parseIDParam(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid monitor id")
		return
	}

	if err := h.monitors.Delete(r.Context(), claims.UserID, claims.Role, id); err != nil {
		h.handleError(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func parseIDParam(r *http.Request) (int64, error) {
	raw := chi.URLParam(r, "id")
	return strconv.ParseInt(raw, 10, 64)
}

// parseWindow reads a ?window= query param like "24h" or "7d" and returns
// the corresponding time.Duration, defaulting to 24h when absent or
// invalid — a dashboard endpoint returning "some reasonable default" for a
// malformed window is friendlier than a hard 400 for what's a convenience
// parameter, not a required identifier like a monitor ID.
func parseWindow(r *http.Request) time.Duration {
	raw := r.URL.Query().Get("window")
	if raw == "" {
		return 24 * time.Hour
	}
	// time.ParseDuration doesn't understand "d" for days, so translate the
	// common case by hand before falling back to ParseDuration for
	// everything else (e.g. "12h", "30m").
	if strings.HasSuffix(raw, "d") {
		if days, err := strconv.Atoi(strings.TrimSuffix(raw, "d")); err == nil && days > 0 {
			return time.Duration(days) * 24 * time.Hour
		}
	}
	if d, err := time.ParseDuration(raw); err == nil && d > 0 {
		return d
	}
	return 24 * time.Hour
}

func parseLimit(r *http.Request, def, max int) int {
	raw := r.URL.Query().Get("limit")
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return def
	}
	if n > max {
		return max
	}
	return n
}

func (h *MonitorHandler) GetChecks(w http.ResponseWriter, r *http.Request) {
	claims, err := appmw.ClaimsFromContext(r.Context())
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	id, err := parseIDParam(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid monitor id")
		return
	}
	window := parseWindow(r)
	limit := parseLimit(r, 50, 500)

	checks, err := h.monitors.Checks(r.Context(), claims.UserID, claims.Role, id, time.Now().Add(-window), limit)
	if err != nil {
		h.handleError(w, err)
		return
	}

	resp := make([]checkResponse, 0, len(checks))
	for _, c := range checks {
		resp = append(resp, toCheckResponse(c))
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *MonitorHandler) GetStats(w http.ResponseWriter, r *http.Request) {
	claims, err := appmw.ClaimsFromContext(r.Context())
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	id, err := parseIDParam(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid monitor id")
		return
	}
	window := parseWindow(r)

	stats, err := h.monitors.Stats(r.Context(), claims.UserID, claims.Role, id, time.Now().Add(-window))
	if err != nil {
		h.handleError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"uptime_percent":       roundTo2(stats.UptimePercent),
		"avg_response_time_ms": stats.AvgResponseTime.Milliseconds(),
		"window_since":         stats.WindowSince.Format(time.RFC3339),
	})
}

func (h *MonitorHandler) GetIncidents(w http.ResponseWriter, r *http.Request) {
	claims, err := appmw.ClaimsFromContext(r.Context())
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	id, err := parseIDParam(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid monitor id")
		return
	}
	limit := parseLimit(r, 20, 200)

	incidents, err := h.monitors.Incidents(r.Context(), claims.UserID, claims.Role, id, limit)
	if err != nil {
		h.handleError(w, err)
		return
	}

	resp := make([]incidentResponse, 0, len(incidents))
	for _, i := range incidents {
		resp = append(resp, toIncidentResponse(i))
	}
	writeJSON(w, http.StatusOK, resp)
}

type checkResponse struct {
	ID             int64   `json:"id"`
	Status         string  `json:"status"`
	StatusCode     int     `json:"status_code,omitempty"`
	ResponseTimeMs int64   `json:"response_time_ms"`
	Error          string  `json:"error,omitempty"`
	TLSExpiresAt   *string `json:"tls_expires_at,omitempty"`
	CheckedAt      string  `json:"checked_at"`
}

func toCheckResponse(c *domain.Check) checkResponse {
	resp := checkResponse{
		ID:             c.ID,
		Status:         string(c.Status),
		StatusCode:     c.StatusCode,
		ResponseTimeMs: c.ResponseTime.Milliseconds(),
		Error:          c.Error,
		CheckedAt:      c.CheckedAt.Format(time.RFC3339),
	}
	if c.TLSExpiresAt != nil {
		s := c.TLSExpiresAt.Format(time.RFC3339)
		resp.TLSExpiresAt = &s
	}
	return resp
}

type incidentResponse struct {
	ID         int64   `json:"id"`
	Status     string  `json:"status"`
	StartedAt  string  `json:"started_at"`
	ResolvedAt *string `json:"resolved_at,omitempty"`
	Cause      string  `json:"cause,omitempty"`
}

func toIncidentResponse(i *domain.Incident) incidentResponse {
	resp := incidentResponse{
		ID:        i.ID,
		Status:    string(i.Status),
		StartedAt: i.StartedAt.Format(time.RFC3339),
		Cause:     i.Cause,
	}
	if i.ResolvedAt != nil {
		s := i.ResolvedAt.Format(time.RFC3339)
		resp.ResolvedAt = &s
	}
	return resp
}

func roundTo2(f float64) float64 {
	return float64(int(f*100)) / 100
}

// handleError maps service/domain errors to HTTP status codes — the same
// "never leak internals" principle as AuthHandler.handleAuthError. Notably,
// domain.ErrNotFound and domain.ErrForbidden both come back as 404, not
// 403, when the requester doesn't own the resource. This is deliberate:
// returning 403 on someone else's monitor ID confirms to an attacker that
// the ID exists and belongs to someone; 404 reveals nothing about whether
// the resource exists at all. This is the same enumeration-prevention
// principle from Login's identical-error-for-wrong-password-or-no-account,
// applied to resource ownership instead of credentials.
func (h *MonitorHandler) handleError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, domain.ErrNotFound), errors.Is(err, domain.ErrForbidden):
		writeError(w, http.StatusNotFound, "monitor not found")
	case errors.Is(err, service.ErrInvalidMonitorType),
		errors.Is(err, validation.ErrInvalidURL),
		errors.Is(err, validation.ErrInvalidName),
		errors.Is(err, validation.ErrIntervalTooShort),
		errors.Is(err, validation.ErrIntervalTooLong),
		errors.Is(err, validation.ErrTimeoutInvalid),
		errors.Is(err, validation.ErrTimeoutExceedsInterval):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		h.log.Error("monitor handler internal error", "error", err)
		writeError(w, http.StatusInternalServerError, "something went wrong")
	}
}
