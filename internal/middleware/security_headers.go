package middleware

import "net/http"

// SecurityHeaders applies standard defensive HTTP headers to every
// response. Each header defends against a specific, well-known attack
// class:
//
//   - HSTS: tells browsers to only ever connect to us over HTTPS for the
//     next year, even if a user types "http://" or clicks an old http link
//     — closes the window for SSL-stripping attacks on subsequent visits.
//   - X-Content-Type-Options: nosniff — stops browsers from guessing
//     ("sniffing") a response's content type and executing it as something
//     more dangerous than what we declared (e.g. treating a JSON response
//     as HTML/JS because it happened to contain a <script> substring).
//   - X-Frame-Options: DENY — this API serves no UI, so it should never be
//     framed by another site; blocks clickjacking-style embedding.
//   - Referrer-Policy: strict-origin-when-cross-origin — avoids leaking
//     full request URLs (which may contain sensitive path/query data) to
//     third-party sites via the Referer header.
//
// We skip a Content-Security-Policy here since SitePulse's API serves JSON,
// not HTML/JS — CSP is meaningful once (if) a frontend is served from this
// same origin.
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		next.ServeHTTP(w, r)
	})
}
