package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"railcache/internal/metrics"
)

type ctxKey int

const requestIDKey ctxKey = iota

// rateLimiter enforces a per-IP token bucket and a global bucket. The per-IP
// bucket is checked FIRST: a single abusive IP must be rejected by its own
// bucket before it can consume a shared global token, otherwise it could drain
// the global budget (which exists to cap DB-fallback load for everyone) while
// being throttled itself.
type rateLimiter struct {
	global *rate.Limiter

	mu    sync.Mutex
	perIP map[string]*ipEntry
	rps   rate.Limit
	burst int

	stop chan struct{}
}

type ipEntry struct {
	lim  *rate.Limiter
	seen time.Time
}

func newRateLimiter(perIPRps float64, perIPBurst int, globalRps float64, globalBurst int) *rateLimiter {
	rl := &rateLimiter{
		global: rate.NewLimiter(rate.Limit(globalRps), globalBurst),
		perIP:  make(map[string]*ipEntry),
		rps:    rate.Limit(perIPRps),
		burst:  perIPBurst,
		stop:   make(chan struct{}),
	}
	go rl.janitor()
	return rl
}

// Close stops the janitor goroutine.
func (rl *rateLimiter) Close() { close(rl.stop) }

func (rl *rateLimiter) limiterFor(ip string) *rate.Limiter {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	e, ok := rl.perIP[ip]
	if !ok {
		e = &ipEntry{lim: rate.NewLimiter(rl.rps, rl.burst)}
		rl.perIP[ip] = e
	}
	e.seen = time.Now()
	return e.lim
}

func (rl *rateLimiter) janitor() {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-rl.stop:
			return
		case <-t.C:
			rl.mu.Lock()
			for ip, e := range rl.perIP {
				if time.Since(e.seen) > 3*time.Minute {
					delete(rl.perIP, ip)
				}
			}
			rl.mu.Unlock()
		}
	}
}

// Middleware returns the rate-limiting handler (per-IP before global).
func (rl *rateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !rl.limiterFor(clientIP(r)).Allow() {
			http.Error(w, "rate limited", http.StatusTooManyRequests)
			return
		}
		if !rl.global.Allow() {
			http.Error(w, "rate limited (global)", http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// requestID assigns/propagates an X-Request-Id and stashes it in the context.
func requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-Id")
		if id == "" {
			id = newRequestID()
		}
		w.Header().Set("X-Request-Id", id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey, id)))
	})
}

// timeout bounds each request's total lifetime.
func timeout(d time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), d)
			defer cancel()
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// recoverer turns a panic into a 500 instead of crashing the server.
func recoverer(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					log.Error("panic recovered", "err", rec, "path", r.URL.Path,
						"request_id", RequestIDFrom(r.Context()))
					http.Error(w, "internal error", http.StatusInternalServerError)
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// requestLogger logs each request and records its latency in the histogram.
func requestLogger(log *slog.Logger, m *metrics.Metrics) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			sw := &statusWriter{ResponseWriter: w, status: 200}
			next.ServeHTTP(sw, r)
			dur := time.Since(start)
			m.ObserveLatency(dur)
			log.Info("request", "method", r.Method, "path", r.URL.Path,
				"status", sw.status, "dur_ms", dur.Milliseconds(),
				"request_id", RequestIDFrom(r.Context()))
		})
	}
}

// RequestIDFrom extracts the request id (empty if absent).
func RequestIDFrom(ctx context.Context) string {
	if id, ok := ctx.Value(requestIDKey).(string); ok {
		return id
	}
	return ""
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func clientIP(r *http.Request) string {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

func newRequestID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "unknown"
	}
	return hex.EncodeToString(b)
}
