package httpapi

import (
	"bytes"
	"context"
	"crypto/subtle"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"net/http/pprof"
	"time"

	"github.com/go-chi/chi/v5"

	"railcache/internal/config"
	"railcache/internal/herd"
	"railcache/internal/metrics"
	"railcache/internal/search"
)

//go:embed templates/*.html
var templatesFS embed.FS

// Pinger reports datastore health.
type Pinger interface {
	Ping(ctx context.Context) error
}

// Server holds handler dependencies.
type Server struct {
	svc       *search.Service
	validator *search.Validator
	m         *metrics.Metrics
	log       *slog.Logger
	tmpl      *template.Template
	db        Pinger
	redis     Pinger
	cfg       config.Config
}

// NewServer builds the server and returns the public and internal routers.
//
// The two routers are a deliberate boundary: user traffic hits `public`; admin,
// debug, metrics, and pprof live only on `internal`, which the deployment binds
// to a non-public interface. Admin surfaces are never on the public router.
func NewServer(svc *search.Service, v *search.Validator, m *metrics.Metrics, log *slog.Logger, db, redis Pinger, cfg config.Config) (public, internal http.Handler, err error) {
	tmpl, terr := template.ParseFS(templatesFS, "templates/*.html")
	if terr != nil {
		return nil, nil, fmt.Errorf("parse templates: %w", terr)
	}
	s := &Server{svc: svc, validator: v, m: m, log: log, tmpl: tmpl, db: db, redis: redis, cfg: cfg}

	rl := newRateLimiter(cfg.RatePerIP, cfg.RatePerIPBurst, cfg.RateGlobal, cfg.RateGlobalBurst)

	pub := chi.NewRouter()
	pub.Use(recoverer(log), requestID, requestLogger(log, m), timeout(cfg.RequestTimeout), rl.Middleware)
	pub.Get("/", s.handleHTML)
	pub.Get("/search", s.handleHTML)
	pub.Get("/api/search", s.handleAPISearch)
	pub.Get("/livez", s.handleLivez)
	pub.Get("/readyz", s.handleReadyz)

	in := chi.NewRouter()
	in.Use(recoverer(log), requestID, requestLogger(log, m))
	in.Get("/metrics", m.Handler())
	in.Get("/livez", s.handleLivez)
	in.Get("/readyz", s.handleReadyz)
	if cfg.AdminEnabled() {
		in.Group(func(r chi.Router) {
			r.Use(s.requireAdmin)
			r.Post("/admin/invalidate", s.handleInvalidate)
			r.Get("/debug/herd", s.handleHerd)
		})
	} else {
		log.Warn("ADMIN_TOKEN not set: admin and load-test routes are disabled")
	}
	// pprof, internal-only.
	in.HandleFunc("/debug/pprof/", pprof.Index)
	in.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	in.HandleFunc("/debug/pprof/profile", pprof.Profile)
	in.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	in.HandleFunc("/debug/pprof/trace", pprof.Trace)

	return pub, in, nil
}

// requireAdmin enforces a constant-time bearer-token check on admin routes.
func (s *Server) requireAdmin(next http.Handler) http.Handler {
	want := []byte("Bearer " + s.cfg.AdminToken)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := []byte(r.Header.Get("Authorization"))
		if len(got) != len(want) || subtle.ConstantTimeCompare(got, want) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func rawQuery(r *http.Request) search.Query {
	return search.Query{
		From:  r.URL.Query().Get("from"),
		To:    r.URL.Query().Get("to"),
		Date:  r.URL.Query().Get("date"),
		Class: r.URL.Query().Get("class"),
	}
}

// validated parses and validates the query, writing a 400 and returning ok=false
// on failure so a malformed request never becomes a cache key or a DB query.
func (s *Server) validated(w http.ResponseWriter, r *http.Request) (search.Query, bool) {
	q, err := s.validator.Validate(rawQuery(r))
	if err != nil {
		var invalid *search.InvalidInputError
		if errors.As(err, &invalid) {
			http.Error(w, invalid.Error(), http.StatusBadRequest)
			return q, false
		}
		s.log.Error("validation error", "err", err)
		http.Error(w, "validation failed", http.StatusInternalServerError)
		return q, false
	}
	return q, true
}

func (s *Server) handleHTML(w http.ResponseWriter, r *http.Request) {
	data := map[string]any{
		"Classes":  []string{"SL", "3A", "2A"},
		"TookMs":   0.0,
		"DBTookMs": 0.0,
		"Q":        search.Query{From: "NDLS", To: "BCT", Date: defaultDate(), Class: "3A"},
	}
	if hasQueryParams(r) {
		data["DidSearch"] = true
		q, err := s.validator.Validate(rawQuery(r))
		data["Q"] = q
		if err != nil {
			data["Error"] = err.Error()
		} else {
			start := time.Now()
			out, serr := s.svc.Search(r.Context(), q)
			if serr != nil {
				data["Error"] = "search failed"
			} else {
				data["Result"] = out.Result
				data["Cache"] = string(out.Status)
				data["TookMs"] = msSince(start)
				data["DBTookMs"] = float64(out.DBTook.Microseconds()) / 1000.0
				if !out.AsOf.IsZero() {
					data["AsOfSec"] = int(time.Since(out.AsOf).Seconds())
				}
			}
		}
	}
	// Render to a buffer first so a mid-render error can't emit a torn 200.
	var buf bytes.Buffer
	if err := s.tmpl.ExecuteTemplate(&buf, "index.html", data); err != nil {
		s.log.Error("template render", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = buf.WriteTo(w)
}

func (s *Server) handleAPISearch(w http.ResponseWriter, r *http.Request) {
	q, ok := s.validated(w, r)
	if !ok {
		return
	}
	start := time.Now()
	out, err := s.svc.Search(r.Context(), q)
	if err != nil {
		s.log.Error("search", "err", err, "request_id", RequestIDFrom(r.Context()))
		http.Error(w, "search failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("X-Cache", string(out.Status))
	w.Header().Set("Cache-Control", "no-store") // availability must not be cached by intermediaries
	if !out.AsOf.IsZero() {
		w.Header().Set("X-Data-Age-Seconds", fmt.Sprintf("%d", int(time.Since(out.AsOf).Seconds())))
	}
	w.Header().Set("Server-Timing", fmt.Sprintf("db;dur=%.2f, total;dur=%.2f",
		float64(out.DBTook.Microseconds())/1000.0, msSince(start)))
	writeJSON(w, http.StatusOK, map[string]any{
		"result": out.Result,
		"cache":  out.Status,
		"as_of":  out.AsOf,
	})
}

func (s *Server) handleInvalidate(w http.ResponseWriter, r *http.Request) {
	var q search.Query
	if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	if err := s.svc.Invalidate(r.Context(), q); err != nil {
		http.Error(w, "invalidate failed", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "invalidated"})
}

func (s *Server) handleHerd(w http.ResponseWriter, r *http.Request) {
	q, ok := s.validated(w, r)
	if !ok {
		return
	}
	// Bound the load generator so it can never be a self-DoS.
	n := clamp(atoiDefault(r.URL.Query().Get("n"), 1000), 1, s.cfg.HerdMaxN)
	conc := clamp(atoiDefault(r.URL.Query().Get("concurrency"), 200), 1, s.cfg.HerdMaxConcurrency)

	before := s.m.Snapshot()
	rep := herd.Run(r.Context(), n, conc, func(ctx context.Context) (search.Outcome, error) {
		return s.svc.Search(ctx, q)
	})
	after := s.m.Snapshot()
	writeJSON(w, http.StatusOK, map[string]any{
		"herd":             rep,
		"db_fills_delta":   after.DBFills - before.DBFills,
		"suppressed_delta": after.HerdSuppressed - before.HerdSuppressed,
		"note":             "db_fills_delta should be ~1 for a single cold hot key",
	})
}

// handleLivez is pure liveness: the process is up. It never depends on
// datastores, so a dependency blip can't make an orchestrator kill a healthy
// pod.
func (s *Server) handleLivez(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "alive"})
}

// handleReadyz is degraded-aware. The service can serve as long as EITHER store
// works: Redis for hits, Postgres for fallback. It reports 200 with a mode, and
// only 503 when BOTH are gone — pulling traffic from an instance that can still
// serve is worse than serving degraded.
func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), time.Second)
	defer cancel()
	redisOK := s.redis.Ping(ctx) == nil
	dbOK := s.db.Ping(ctx) == nil

	mode := "full"
	switch {
	case redisOK && dbOK:
		mode = "full"
	case redisOK && !dbOK:
		mode = "cache-only"
	case !redisOK && dbOK:
		mode = "no-cache"
	default:
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"ready": false, "redis": redisOK, "db": dbOK})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ready": true, "mode": mode, "redis": redisOK, "db": dbOK})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func hasQueryParams(r *http.Request) bool {
	q := r.URL.Query()
	return q.Get("from") != "" || q.Get("to") != "" || q.Get("date") != "" || q.Get("class") != ""
}

func atoiDefault(s string, def int) int {
	var n int
	if _, err := fmt.Sscanf(s, "%d", &n); err == nil && n > 0 {
		return n
	}
	return def
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func msSince(t time.Time) float64 { return float64(time.Since(t).Microseconds()) / 1000.0 }

// defaultDate returns a demo-friendly near-future date so the landing page's
// prefilled search stays valid over time instead of drifting past a hardcoded day.
func defaultDate() string { return time.Now().AddDate(0, 0, 2).Format("2006-01-02") }
