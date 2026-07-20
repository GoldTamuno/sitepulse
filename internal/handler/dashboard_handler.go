package handler

import (
	"net/http"
	"time"

	appmw "github.com/yourname/sitepulse/internal/middleware"
	"github.com/yourname/sitepulse/internal/service"
)

type DashboardHandler struct {
	dashboard *service.DashboardService
}

func NewDashboardHandler(dashboard *service.DashboardService) *DashboardHandler {
	return &DashboardHandler{dashboard: dashboard}
}

// Summary is intentionally open to every authenticated role, including
// viewer — unlike monitor writes, "see the overview of my own monitors" is
// exactly the read-only capability a viewer role exists for.
func (h *DashboardHandler) Summary(w http.ResponseWriter, r *http.Request) {
	claims, err := appmw.ClaimsFromContext(r.Context())
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	window := parseWindow(r)
	summary, err := h.dashboard.Summary(r.Context(), claims.UserID, window)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "something went wrong")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"total_monitors":       summary.TotalMonitors,
		"healthy_count":        summary.HealthyCount,
		"unhealthy_count":      summary.UnhealthyCount,
		"pending_count":        summary.PendingCount,
		"avg_uptime_percent":   roundTo2(summary.AvgUptimePercent),
		"avg_response_time_ms": summary.AvgResponseTime.Milliseconds(),
		"window_since":         summary.WindowSince.Format(time.RFC3339),
	})
}
