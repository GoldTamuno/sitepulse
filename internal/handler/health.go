package handler

import (
	"net/http"
)

// HealthHandler exposes SitePulse's own liveness endpoint — not to be
// confused with the checks SitePulse runs against *other* people's sites.
// Useful for Railway/Docker health checks and load balancers.
type HealthHandler struct{}

func NewHealthHandler() *HealthHandler {
	return &HealthHandler{}
}

func (h *HealthHandler) Routes() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}
