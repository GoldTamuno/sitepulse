package middleware

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/yourname/sitepulse/internal/domain"
	"github.com/yourname/sitepulse/internal/security"
)

type contextKey string

const claimsContextKey contextKey = "claims"

// Authenticate verifies the Bearer access token on every request to a
// protected route and stashes the parsed claims in the request context for
// downstream handlers/middleware (like RequireRole) to read. A missing or
// invalid token is a 401 here, before the request ever reaches a handler —
// handlers should never need to think about "is this request
// authenticated," only "what does this authenticated user want."
func Authenticate(tokens *security.TokenIssuer) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			header := r.Header.Get("Authorization")
			const prefix = "Bearer "
			if !strings.HasPrefix(header, prefix) {
				writeUnauthorized(w)
				return
			}
			rawToken := strings.TrimPrefix(header, prefix)

			claims, err := tokens.VerifyAccessToken(rawToken)
			if err != nil {
				writeUnauthorized(w)
				return
			}

			ctx := context.WithValue(r.Context(), claimsContextKey, claims)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RequireRole enforces RBAC: the caller's role (from the verified JWT
// claims already placed in context by Authenticate) must be one of the
// allowed roles, or the request is rejected with 403. This must run after
// Authenticate in the middleware chain — it panics-safe no-ops into a 401
// if claims aren't present, rather than assuming Authenticate always ran,
// because relying on call-order without a safety check is exactly the kind
// of thing that quietly breaks authorization when someone reorders
// middleware six months from now.
//
// This is the actual authorization boundary the project's security
// requirements call for: it runs server-side, on every request, against
// the role embedded in a token we cryptographically verified ourselves —
// never a role the client merely claims in a request body or query param.
func RequireRole(roles ...domain.Role) func(http.Handler) http.Handler {
	allowed := make(map[domain.Role]bool, len(roles))
	for _, r := range roles {
		allowed[r] = true
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims, err := ClaimsFromContext(r.Context())
			if err != nil {
				writeUnauthorized(w)
				return
			}
			if !allowed[claims.Role] {
				http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

var ErrNoClaims = errors.New("middleware: no auth claims in context")

// ClaimsFromContext lets handlers read who's making the request (e.g. to
// scope a query to the caller's own resources) without any of them
// re-parsing tokens themselves.
func ClaimsFromContext(ctx context.Context) (*security.Claims, error) {
	claims, ok := ctx.Value(claimsContextKey).(*security.Claims)
	if !ok || claims == nil {
		return nil, ErrNoClaims
	}
	return claims, nil
}

func writeUnauthorized(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	if _, err := w.Write([]byte(`{"error":"unauthorized"}`)); err != nil {
		slog.Default().Error("failed to write unauthorized response", "error", err)
	}
}
