// Package obs is a lightweight observability shim (metrics + structured logs).
// It intentionally mirrors the shape of instascale-observability/internal/obs
// so a full port drops in without touching call sites (reuse gate, T6). Counters
// use expvar so /debug/vars and /metrics can expose them with zero deps.
package obs

import (
	"expvar"
	"log/slog"
	"os"
	"sync/atomic"
	"time"
)

// Metrics holds the control-plane counters and a coarse latency accumulator.
type Metrics struct {
	Authorized *expvar.Int
	Denied     *expvar.Int
	Heartbeats *expvar.Int
	Shed       *expvar.Int
	Reaped     *expvar.Int
	latNanos   atomic.Int64
	latCount   atomic.Int64
	Log        *slog.Logger
}

// New builds a Metrics with published expvar counters. Counter registration is
// idempotent so multiple instances (e.g. across tests) don't panic on the
// global expvar registry.
func New() *Metrics {
	return &Metrics{
		Authorized: intVar("ls_authorized_total"),
		Denied:     intVar("ls_denied_total"),
		Heartbeats: intVar("ls_heartbeats_total"),
		Shed:       intVar("ls_shed_total"),
		Reaped:     intVar("ls_reaped_total"),
		Log:        slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})),
	}
}

// intVar returns the published expvar.Int for name, publishing it if absent.
func intVar(name string) *expvar.Int {
	if v := expvar.Get(name); v != nil {
		if iv, ok := v.(*expvar.Int); ok {
			return iv
		}
	}
	return expvar.NewInt(name)
}

// Observe records a request latency (coarse mean; full histogram is the ported
// instascale obs layer's job).
func (m *Metrics) Observe(d time.Duration) {
	m.latNanos.Add(int64(d))
	m.latCount.Add(1)
}

// MeanLatency returns the mean observed latency so far.
func (m *Metrics) MeanLatency() time.Duration {
	n := m.latCount.Load()
	if n == 0 {
		return 0
	}
	return time.Duration(m.latNanos.Load() / n)
}
