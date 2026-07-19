package middleware

import (
	"net"
	"net/http"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// RateLimiter is an in-memory, per-IP token bucket limiter.
//
// Why in-memory and not Redis, given Redis is in the planned stack? Because
// this app runs as a single instance until the deployment phase — an
// in-memory limiter is correct and sufficient right now, and adding a
// Redis dependency before anything needs its actual value (shared state
// across instances) would be complexity with no payoff yet. The trade-off
// to know: this limiter's state is per-process, so if SitePulse is ever
// scaled horizontally (multiple instances behind a load balancer), each
// instance enforces its own independent limit — an attacker distributed
// across instances could get N times the effective rate. That's exactly
// the point where this gets swapped for a Redis-backed limiter (same
// interface, different backing store) — flagged here so it's a deliberate
// future change, not a surprise.
type RateLimiter struct {
	mu       sync.Mutex
	visitors map[string]*rate.Limiter
	rate     rate.Limit
	burst    int
}

// NewRateLimiter creates a limiter allowing `burst` requests immediately,
// refilling at `r` requests/second thereafter, per client IP.
func NewRateLimiter(r rate.Limit, burst int) *RateLimiter {
	rl := &RateLimiter{
		visitors: make(map[string]*rate.Limiter),
		rate:     r,
		burst:    burst,
	}
	go rl.cleanupLoop()
	return rl
}

func (rl *RateLimiter) getLimiter(ip string) *rate.Limiter {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	limiter, exists := rl.visitors[ip]
	if !exists {
		limiter = rate.NewLimiter(rl.rate, rl.burst)
		rl.visitors[ip] = limiter
	}
	return limiter
}

// cleanupLoop prevents the visitors map from growing unbounded as new IPs
// show up over the app's lifetime — without this, this map is a slow
// memory leak. We don't track "last seen" per-IP precisely here for
// simplicity; a periodic full reset is good enough for an auth-endpoint
// limiter where a little jitter in enforcement timing doesn't matter.
func (rl *RateLimiter) cleanupLoop() {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()
	for range ticker.C {
		rl.mu.Lock()
		rl.visitors = make(map[string]*rate.Limiter)
		rl.mu.Unlock()
	}
}

// Limit returns middleware enforcing the configured rate per client IP.
// Intended for auth endpoints (register/login/refresh) to blunt
// credential-stuffing and brute-force attempts.
func (rl *RateLimiter) Limit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := clientIP(r)
		if !rl.getLimiter(ip).Allow() {
			w.Header().Set("Retry-After", "60")
			http.Error(w, `{"error":"too many requests, please try again later"}`, http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// clientIP extracts the request's IP, preferring RemoteAddr over trusting
// client-supplied headers like X-Forwarded-For by default — spoofing a
// forwarded-for header is trivial for an attacker trying to evade
// per-IP limiting. If SitePulse sits behind a trusted reverse proxy
// (Railway does terminate TLS in front of the app), that proxy's own
// header-setting behavior should be confirmed before trusting XFF here.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
