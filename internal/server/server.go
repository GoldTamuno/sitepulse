// Package server wires the HTTP router together. It knows about handlers
// and middleware, but nothing about business logic or SQL — that
// separation is what lets us test handlers with a fake service and swap
// routers later without touching anything below this layer.
package server

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"golang.org/x/time/rate"

	"github.com/yourname/sitepulse/internal/domain"
	"github.com/yourname/sitepulse/internal/handler"
	appmw "github.com/yourname/sitepulse/internal/middleware"
	"github.com/yourname/sitepulse/internal/security"
)

// Handlers bundles every handler the router needs. As we add phases
// (monitors, dashboard) this struct grows — main.go constructs the real
// ones (backed by Postgres) and passes them in here. The router never
// knows or cares what's behind the interface.
type Handlers struct {
	Health    *handler.HealthHandler
	Auth      *handler.AuthHandler
	Monitor   *handler.MonitorHandler
	Dashboard *handler.DashboardHandler
	User      *handler.UserHandler
	Docs      *handler.DocsHandler
}

func NewRouter(log *slog.Logger, h Handlers, tokens *security.TokenIssuer) http.Handler {
	r := chi.NewRouter()

	// --- Global middleware stack ---
	// Order matters: RequestID must run first so every later middleware
	// (including our logger) can see it. Recoverer must wrap everything
	// downstream so a panic anywhere in a handler doesn't crash the whole
	// process — it recovers, logs, and returns a 500 for that one request.
	r.Use(chimw.RequestID)
	r.Use(chimw.RealIP)
	r.Use(appmw.RequestLogger(log))
	r.Use(appmw.SecurityHeaders)
	r.Use(chimw.Recoverer)
	r.Use(chimw.Timeout(30 * time.Second))

	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   []string{"*"}, // tighten to a real allowlist before production use
		AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type"},
		AllowCredentials: true, // required so browsers send the refresh-token cookie cross-origin
		MaxAge:           300,
	}))

	r.Get("/healthz", h.Health.Routes())

	// Docs are intentionally public — an API's documentation shouldn't
	// require an account to read, same reasoning as /healthz.
	r.Get("/docs", h.Docs.UI)
	r.Get("/docs/openapi.yaml", h.Docs.Spec)

	// Auth endpoints get their own, stricter rate limiter (5 req/min
	// sustained, burst of 10) than the rest of the API — these are the
	// endpoints credential-stuffing and brute-force attacks specifically
	// target, so they warrant a tighter budget than general API traffic.
	authLimiter := appmw.NewRateLimiter(rate.Every(12*time.Second), 10)

	r.Route("/api/v1", func(r chi.Router) {
		r.Route("/auth", func(r chi.Router) {
			r.Use(authLimiter.Limit)
			r.Post("/register", h.Auth.Register)
			r.Post("/login", h.Auth.Login)
			r.Post("/refresh", h.Auth.Refresh)
			r.Post("/logout", h.Auth.Logout)
		})

		// Example of the pattern later phases follow: wrap a sub-route in
		// Authenticate (must be logged in) and, where needed, RequireRole
		// (must have a specific role) — e.g. monitor creation restricted
		// to operator/admin, dashboard reads open to any authenticated role.
		r.Group(func(r chi.Router) {
			r.Use(appmw.Authenticate(tokens))

			r.Route("/monitors", func(r chi.Router) {
				// Reads: any authenticated role (viewer included) can list
				// and view their own monitors and their history/stats.
				r.Get("/", h.Monitor.List)
				r.Get("/{id}", h.Monitor.Get)
				r.Get("/{id}/checks", h.Monitor.GetChecks)
				r.Get("/{id}/stats", h.Monitor.GetStats)
				r.Get("/{id}/incidents", h.Monitor.GetIncidents)

				// Writes: gated to operator/admin. A viewer authenticating
				// successfully is not the same as a viewer being allowed
				// to create or mutate monitors — this is RequireRole doing
				// its job at the route level, on top of (not instead of)
				// the per-resource ownership check inside MonitorService.
				r.Group(func(r chi.Router) {
					r.Use(appmw.RequireRole(domain.RoleOperator, domain.RoleAdmin))
					r.Post("/", h.Monitor.Create)
					r.Put("/{id}", h.Monitor.Update)
					r.Delete("/{id}", h.Monitor.Delete)
				})
			})

			r.Get("/dashboard", h.Dashboard.Summary)

			// /me: any authenticated role — self-service profile lookup.
			r.Get("/me", h.User.Me)

			// Admin-only user management. A dedicated /admin prefix, rather
			// than folding this into /users at the top level, keeps the
			// route tree itself documenting the access boundary — anyone
			// reading the route list can see admin-only surface area
			// without having to open server.go or the handler to check.
			r.Route("/admin/users", func(r chi.Router) {
				r.Use(appmw.RequireRole(domain.RoleAdmin))
				r.Get("/", h.User.List)
				r.Get("/{id}", h.User.Get)
				r.Patch("/{id}/role", h.User.UpdateRole)
			})
		})
	})

	return r
}
