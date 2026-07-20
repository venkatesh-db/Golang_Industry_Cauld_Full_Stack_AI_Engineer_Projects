package httpx

import (
	"context"
	"net/http"
	"sync/atomic"
	"time"

	"livescale/internal/obs"
	"livescale/internal/token"
)

// priority classifies routes for load shedding (ADR-002). HIGH routes
// (authorize/heartbeat) are protected until hardMax; LOW routes (stats) shed
// at softMax so playback survives while dashboards are sacrificed.
type priority int

const (
	low priority = iota
	high
)

// Admission is a lock-free, priority-aware inflight limiter.
type Admission struct {
	inflight atomic.Int64
	softMax  int64
	hardMax  int64
	metrics  *obs.Metrics
}

func NewAdmission(softMax, hardMax int, m *obs.Metrics) *Admission {
	return &Admission{softMax: int64(softMax), hardMax: int64(hardMax), metrics: m}
}

// guard wraps a handler with recover, latency observation, and priority-aware
// shedding. Order (ADR-002): observe/recover always; shed AFTER admission is
// measured but BEFORE handler work, so shed requests cost ~nothing.
func (a *Admission) guard(p priority, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		defer func() {
			if rec := recover(); rec != nil {
				a.metrics.Log.Error("panic recovered", "path", r.URL.Path, "err", rec)
				http.Error(w, `{"error":"INTERNAL"}`, http.StatusInternalServerError)
			}
			a.metrics.Observe(time.Since(start))
		}()

		n := a.inflight.Add(1)
		defer a.inflight.Add(-1)

		limit := a.hardMax
		if p == low {
			limit = a.softMax
		}
		if n > limit {
			a.metrics.Shed.Add(1)
			w.Header().Set("Retry-After", "1")
			http.Error(w, `{"error":"OVERLOADED"}`, http.StatusServiceUnavailable)
			return
		}
		h(w, r)
	}
}

type claimsKey struct{}

// authed verifies the bearer/token field before the handler runs, injecting
// verified claims into the request context. Verify-only (spec §2).
func (a *Admission) authed(key []byte, h func(http.ResponseWriter, *http.Request, token.Claims)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tok := bearer(r)
		if tok == "" {
			http.Error(w, `{"error":"NO_TOKEN"}`, http.StatusUnauthorized)
			return
		}
		claims, err := token.Verify(key, tok, time.Now())
		if err != nil {
			http.Error(w, `{"error":"UNAUTHORIZED"}`, http.StatusUnauthorized)
			return
		}
		h(w, r.WithContext(context.WithValue(r.Context(), claimsKey{}, claims)), claims)
	}
}

func bearer(r *http.Request) string {
	if h := r.Header.Get("Authorization"); len(h) > 7 && h[:7] == "Bearer " {
		return h[7:]
	}
	return r.Header.Get("X-Playback-Token")
}
