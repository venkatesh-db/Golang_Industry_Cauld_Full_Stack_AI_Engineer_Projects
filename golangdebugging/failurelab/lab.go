// Package failurelab provides bounded, authenticated production-failure
// simulations. It must remain disabled outside an isolated test environment.
package failurelab

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Config defines hard ceilings for intentionally adverse experiments.
type Config struct {
	Enabled             bool
	Token               string
	Logger              *slog.Logger
	Database            *pgxpool.Pool
	HTTPClient          *http.Client
	RetryTargetURL      string
	MaxLeakedGoroutines int
	MaxMemoryMiB        int
	MaxDBHolders        int
	MaxDuration         time.Duration
}

// Lab owns the retained memory and goroutines created by experiments.
type Lab struct {
	config Config

	mu          sync.Mutex
	leakGate    chan struct{}
	leaked      int
	allocations [][]byte
}

// New returns a failure lab with conservative hard limits.
func New(config Config) *Lab {
	if config.Logger == nil {
		config.Logger = slog.Default()
	}
	if config.HTTPClient == nil {
		config.HTTPClient = &http.Client{Timeout: 2 * time.Second}
	}
	if config.MaxLeakedGoroutines <= 0 {
		config.MaxLeakedGoroutines = 256
	}
	if config.MaxMemoryMiB <= 0 {
		config.MaxMemoryMiB = 128
	}
	if config.MaxDBHolders <= 0 {
		config.MaxDBHolders = 32
	}
	if config.MaxDuration <= 0 {
		config.MaxDuration = 30 * time.Second
	}
	return &Lab{config: config, leakGate: make(chan struct{})}
}

// Handler serves GET status and POST experiment/reset requests. It returns 404
// while disabled so production deployments do not advertise the feature.
func (l *Lab) Handler() http.Handler {
	return http.HandlerFunc(l.serveHTTP)
}

func (l *Lab) serveHTTP(writer http.ResponseWriter, request *http.Request) {
	if !l.config.Enabled {
		http.NotFound(writer, request)
		return
	}
	if l.config.Token == "" || subtle.ConstantTimeCompare([]byte(request.Header.Get("X-Failure-Lab-Token")), []byte(l.config.Token)) != 1 {
		http.Error(writer, "unauthorized", http.StatusUnauthorized)
		return
	}
	experiment := request.PathValue("experiment")
	if request.Method == http.MethodGet && experiment == "status" {
		l.writeStatus(writer)
		return
	}
	if request.Method != http.MethodPost {
		writer.Header().Set("Allow", http.MethodPost)
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	switch experiment {
	case "goroutine-leak":
		l.leakGoroutines(writer, request)
	case "memory-pressure":
		l.allocateMemory(writer, request)
	case "db-pool-exhaustion":
		l.exhaustDatabase(writer, request)
	case "retry-storm":
		l.retryStorm(writer, request)
	case "slow-dependency":
		l.slowDependency(writer, request)
	case "always-fail":
		http.Error(writer, "intentional failure", http.StatusServiceUnavailable)
	case "reset":
		l.reset(writer)
	default:
		http.NotFound(writer, request)
	}
}

func (l *Lab) leakGoroutines(writer http.ResponseWriter, request *http.Request) {
	count := boundedInt(request, "count", 25, 1, l.config.MaxLeakedGoroutines)
	l.mu.Lock()
	remaining := l.config.MaxLeakedGoroutines - l.leaked
	if count > remaining {
		count = remaining
	}
	gate := l.leakGate
	l.leaked += count
	l.mu.Unlock()
	for range count {
		go func() { <-gate }()
	}
	l.config.Logger.WarnContext(request.Context(), "failure lab created blocked goroutines", "count", count)
	l.writeJSON(writer, http.StatusAccepted, map[string]any{"created": count})
}

func (l *Lab) allocateMemory(writer http.ResponseWriter, request *http.Request) {
	requested := boundedInt(request, "mib", 16, 1, l.config.MaxMemoryMiB)
	l.mu.Lock()
	used := 0
	for _, allocation := range l.allocations {
		used += len(allocation) >> 20
	}
	remaining := l.config.MaxMemoryMiB - used
	if requested > remaining {
		requested = remaining
	}
	if requested > 0 {
		allocation := make([]byte, requested<<20)
		for offset := 0; offset < len(allocation); offset += 4096 {
			allocation[offset] = 1
		}
		l.allocations = append(l.allocations, allocation)
	}
	l.mu.Unlock()
	l.config.Logger.WarnContext(request.Context(), "failure lab retained memory", "mib", requested)
	l.writeJSON(writer, http.StatusAccepted, map[string]any{"allocated_mib": requested})
}

func (l *Lab) exhaustDatabase(writer http.ResponseWriter, request *http.Request) {
	if l.config.Database == nil {
		http.Error(writer, "database is not configured", http.StatusServiceUnavailable)
		return
	}
	count := boundedInt(request, "connections", 8, 1, l.config.MaxDBHolders)
	hold := boundedDuration(request, "duration_ms", 10*time.Second, l.config.MaxDuration)
	for range count {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), hold+5*time.Second)
			defer cancel()
			connection, err := l.config.Database.Acquire(ctx)
			if err != nil {
				l.config.Logger.Error("failure lab could not acquire database connection", "error", err)
				return
			}
			defer connection.Release()
			timer := time.NewTimer(hold)
			defer timer.Stop()
			select {
			case <-ctx.Done():
			case <-timer.C:
			}
		}()
	}
	l.config.Logger.WarnContext(request.Context(), "failure lab started database pool exhaustion", "connections", count, "duration", hold)
	l.writeJSON(writer, http.StatusAccepted, map[string]any{"holders": count, "duration": hold.String()})
}

func (l *Lab) retryStorm(writer http.ResponseWriter, request *http.Request) {
	if l.config.RetryTargetURL == "" {
		http.Error(writer, "retry target is not configured", http.StatusServiceUnavailable)
		return
	}
	attempts := boundedInt(request, "attempts", 10, 1, 25)
	failures := 0
	for range attempts {
		downstreamRequest, err := http.NewRequestWithContext(request.Context(), http.MethodPost, l.config.RetryTargetURL, nil)
		if err != nil {
			failures++
			continue
		}
		downstreamRequest.Header.Set("X-Failure-Lab-Token", l.config.Token)
		response, err := l.config.HTTPClient.Do(downstreamRequest)
		if err != nil {
			failures++
			continue
		}
		_, _ = io.Copy(io.Discard, response.Body)
		_ = response.Body.Close()
		if response.StatusCode >= 500 {
			failures++
		}
	}
	l.config.Logger.WarnContext(request.Context(), "failure lab completed retry storm", "attempts", attempts, "failures", failures)
	l.writeJSON(writer, http.StatusOK, map[string]any{"attempts": attempts, "failures": failures})
}

func (l *Lab) slowDependency(writer http.ResponseWriter, request *http.Request) {
	duration := boundedDuration(request, "duration_ms", 2*time.Second, l.config.MaxDuration)
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-request.Context().Done():
		return
	case <-timer.C:
		l.writeJSON(writer, http.StatusOK, map[string]any{"slept": duration.String()})
	}
}

func (l *Lab) reset(writer http.ResponseWriter) {
	l.mu.Lock()
	close(l.leakGate)
	l.leakGate = make(chan struct{})
	l.leaked = 0
	l.allocations = nil
	l.mu.Unlock()
	l.writeJSON(writer, http.StatusOK, map[string]any{"reset": true})
}

func (l *Lab) writeStatus(writer http.ResponseWriter) {
	l.mu.Lock()
	memoryBytes := 0
	for _, allocation := range l.allocations {
		memoryBytes += len(allocation)
	}
	status := map[string]any{"leaked_goroutines": l.leaked, "retained_memory_bytes": memoryBytes}
	l.mu.Unlock()
	l.writeJSON(writer, http.StatusOK, status)
}

func (l *Lab) writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

func boundedInt(request *http.Request, name string, fallback, minimum, maximum int) int {
	value, err := strconv.Atoi(request.URL.Query().Get(name))
	if err != nil {
		return fallback
	}
	if value < minimum {
		return minimum
	}
	if value > maximum {
		return maximum
	}
	return value
}

func boundedDuration(request *http.Request, name string, fallback, maximum time.Duration) time.Duration {
	milliseconds := boundedInt(request, name, int(fallback/time.Millisecond), 1, int(maximum/time.Millisecond))
	return time.Duration(milliseconds) * time.Millisecond
}
