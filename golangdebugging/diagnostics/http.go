package diagnostics

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"sync"
	"time"
)

// Authorize decides whether an internal caller may access diagnostics.
// Returning false produces a 401 response. Do not expose this endpoint on a
// public listener; goroutine stacks and logs can reveal internal details.
type Authorize func(*http.Request) bool

// HTTPOptions configures the protected incident-snapshot HTTP endpoint.
type HTTPOptions struct {
	Service           *Service
	Authorize         Authorize
	AllowGoroutines   bool
	RequestsPerMinute int
	MaxTrackedClients int
	Now               func() time.Time
}

// NewHandler returns an HTTP handler for GET /internal/diagnostics/snapshot.
// The route is left to the caller so it can live behind the application's
// existing private router and authentication middleware.
func NewHandler(options HTTPOptions) http.Handler {
	if options.Service == nil {
		panic("diagnostics: an incident snapshot service is required")
	}
	if options.Authorize == nil {
		panic("diagnostics: an authorizer is required")
	}
	if options.RequestsPerMinute <= 0 {
		options.RequestsPerMinute = 6
	}
	if options.MaxTrackedClients <= 0 {
		options.MaxTrackedClients = 1024
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	return &snapshotHandler{
		service:         options.Service,
		authorize:       options.Authorize,
		allowGoroutines: options.AllowGoroutines,
		limiter: rateLimiter{
			limit:      options.RequestsPerMinute,
			maxClients: options.MaxTrackedClients,
			now:        options.Now,
			seen:       make(map[string]clientWindow),
		},
	}
}

type snapshotHandler struct {
	service         *Service
	authorize       Authorize
	allowGoroutines bool
	limiter         rateLimiter
}

func (h *snapshotHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		writer.Header().Set("Allow", http.MethodGet)
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !h.authorize(request) {
		http.Error(writer, "unauthorized", http.StatusUnauthorized)
		return
	}
	if !h.limiter.allow(clientID(request)) {
		writer.Header().Set("Retry-After", "60")
		http.Error(writer, "diagnostic snapshot rate limit exceeded", http.StatusTooManyRequests)
		return
	}

	includeGoroutines := h.allowGoroutines && request.URL.Query().Get("goroutines") == "true"
	snapshot, err := h.service.Capture(request.Context(), includeGoroutines)
	if err != nil {
		if errors.Is(err, ErrSnapshotInProgress) {
			writer.Header().Set("Retry-After", "5")
			http.Error(writer, "goroutine snapshot already in progress", http.StatusTooManyRequests)
			return
		}
		http.Error(writer, "unable to capture diagnostic snapshot", http.StatusServiceUnavailable)
		return
	}

	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	if err := json.NewEncoder(writer).Encode(snapshot); err != nil {
		return
	}
}

type clientWindow struct {
	started time.Time
	count   int
}

type rateLimiter struct {
	mu         sync.Mutex
	limit      int
	maxClients int
	now        func() time.Time
	seen       map[string]clientWindow
	overflow   clientWindow
}

func (l *rateLimiter) allow(client string) bool {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()

	window, found := l.seen[client]
	if !found && len(l.seen) >= l.maxClients {
		for id, candidate := range l.seen {
			if now.Sub(candidate.started) >= time.Minute {
				delete(l.seen, id)
			}
		}
		window, found = l.seen[client]
		if !found && len(l.seen) >= l.maxClients {
			return l.allowWindow(&l.overflow, now)
		}
	}
	if !l.allowWindow(&window, now) {
		return false
	}
	l.seen[client] = window
	return true
}

func (l *rateLimiter) allowWindow(window *clientWindow, now time.Time) bool {
	if window.started.IsZero() || now.Sub(window.started) >= time.Minute {
		*window = clientWindow{started: now, count: 1}
		return true
	}
	if window.count >= l.limit {
		return false
	}
	window.count++
	return true
}

func clientID(request *http.Request) string {
	host, _, err := net.SplitHostPort(request.RemoteAddr)
	if err == nil && host != "" {
		return host
	}
	if request.RemoteAddr != "" {
		return request.RemoteAddr
	}
	return "unknown"
}
