package chaos

import (
	"net/http"
	"strconv"
)

// Register wires POST/DELETE /chaos/{mode} onto the mux, but ONLY when enabled.
// When CHAOS_ENABLED is false the routes are never registered, so the failure
// laboratory cannot fire under normal load (Phase 2 guardrail).
func (r *Registry) Register(mux *http.ServeMux) {
	if !r.enabled {
		return
	}
	mux.HandleFunc("POST /chaos/{mode}", r.trigger)
	mux.HandleFunc("DELETE /chaos/{mode}", r.reset)
}

func (r *Registry) trigger(w http.ResponseWriter, req *http.Request) {
	mode := req.PathValue("mode")
	switch mode {
	case "goroutine-leak":
		r.GoroutineLeak(qInt(req, "n", 1000))
	case "mem-pressure":
		r.MemPressure(qInt(req, "mb", 256))
	case "slow-dep":
		r.SlowDep(qInt(req, "ms", 800))
	case "retry-storm":
		r.RetryStorm(true)
	case "db-pool-exhaust":
		r.DBPoolExhaust(req.Context(), qInt(req, "n", 20))
	default:
		http.Error(w, "unknown chaos mode: "+mode, http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusAccepted)
	_, _ = w.Write([]byte("chaos triggered: " + mode + "\n"))
}

func (r *Registry) reset(w http.ResponseWriter, req *http.Request) {
	// Reset is global by design — one call restores health from any mode.
	r.Reset()
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("chaos reset\n"))
}

func qInt(req *http.Request, key string, def int) int {
	if v := req.URL.Query().Get(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}
