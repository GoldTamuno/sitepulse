package middleware

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5/middleware"
)

// RequestLogger returns Chi middleware that logs one structured line per
// request. This is a good first example of the middleware pattern you'll
// reuse for auth and rate limiting: a middleware is just a function that
// takes an http.Handler and returns a wrapped http.Handler, so it can run
// code before and after the real handler runs.
//
// We wrap chi's response writer (middleware.WrapResponseWriter) to capture
// the status code — the stdlib http.ResponseWriter doesn't expose what
// status was written after the fact, so without this wrapper we'd have no
// way to log "responded 500" after the handler returns.
func RequestLogger(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)

			next.ServeHTTP(ww, r)

			log.Info(
				"http_request",
				"method", r.Method,
				"path", r.URL.Path,
				"status", ww.Status(),
				"bytes", ww.BytesWritten(),
				"duration_ms", time.Since(start).Milliseconds(),
				"request_id", middleware.GetReqID(r.Context()),
			)
		})
	}
}
