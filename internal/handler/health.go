package handler

import (
	"encoding/json"
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
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}
}
