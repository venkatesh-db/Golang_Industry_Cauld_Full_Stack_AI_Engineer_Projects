// Package metrics holds process-local counters, a latency histogram, and
// registerable gauges that make the cache pattern observable. It is exposed as
// JSON and Prometheus text at /metrics on the internal listener.
//
// A real deployment would use prometheus/client_golang; this hand-rolled
// version keeps the dependency surface small and the mechanics legible for
// study, while emitting the same conceptual signals (counters + histogram +
// gauges) an operator needs to run the service.
package metrics

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// Metrics is a set of lock-free counters plus a latency histogram and gauges.
type Metrics struct {
	Hits           atomic.Int64 // fresh cache hits
	Stale          atomic.Int64 // stale served (background refresh triggered)
	Misses         atomic.Int64 // cold fills served from Postgres
	HerdSuppressed atomic.Int64 // losers that reused another request's fill
	Fallbacks      atomic.Int64 // Redis down/open → direct Postgres
	DBFills        atomic.Int64 // cold-fill DB queries (the herd-collapse metric)
	Refreshes      atomic.Int64 // background revalidation DB queries
	Errors         atomic.Int64 // request errored

	latency *histogram

	mu     sync.RWMutex
	gauges map[string]func() float64
}

// New returns a zeroed Metrics.
func New() *Metrics {
	return &Metrics{
		latency: newHistogram([]float64{1, 2, 5, 10, 20, 50, 100, 200, 500, 1000}),
		gauges:  map[string]func() float64{},
	}
}

// ObserveLatency records a served-request latency.
func (m *Metrics) ObserveLatency(d time.Duration) {
	m.latency.observe(float64(d.Microseconds()) / 1000.0)
}

// RegisterGauge attaches a named gauge sampled on each scrape (pool stats,
// breaker state, station-set size, …).
func (m *Metrics) RegisterGauge(name string, f func() float64) {
	m.mu.Lock()
	m.gauges[name] = f
	m.mu.Unlock()
}

// Snapshot is a point-in-time counter copy for JSON serialization.
type Snapshot struct {
	Hits           int64   `json:"hits"`
	Stale          int64   `json:"stale"`
	Misses         int64   `json:"misses"`
	HerdSuppressed int64   `json:"herd_suppressed"`
	Fallbacks      int64   `json:"fallbacks"`
	DBFills        int64   `json:"db_fills"`
	Refreshes      int64   `json:"refreshes"`
	Errors         int64   `json:"errors"`
	Served         int64   `json:"served"`
	DBQueries      int64   `json:"db_queries"` // DBFills + Refreshes + Fallbacks
	HitRatio       float64 `json:"hit_ratio"`  // cache-served / served
}

// Snapshot reads all counters and derives ratios.
func (m *Metrics) Snapshot() Snapshot {
	h, st := m.Hits.Load(), m.Stale.Load()
	miss, sup := m.Misses.Load(), m.HerdSuppressed.Load()
	fb := m.Fallbacks.Load()
	served := h + st + miss + sup + fb
	cacheServed := h + st + sup
	var ratio float64
	if served > 0 {
		ratio = float64(cacheServed) / float64(served)
	}
	return Snapshot{
		Hits: h, Stale: st, Misses: miss, HerdSuppressed: sup, Fallbacks: fb,
		DBFills: m.DBFills.Load(), Refreshes: m.Refreshes.Load(), Errors: m.Errors.Load(),
		Served: served, DBQueries: m.DBFills.Load() + m.Refreshes.Load() + fb, HitRatio: ratio,
	}
}

// Handler serves counters+gauges as JSON, or Prometheus text when ?format=prom.
func (m *Metrics) Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s := m.Snapshot()
		if r.URL.Query().Get("format") == "prom" {
			m.writeProm(w, s)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"counters": s,
			"latency":  m.latency.snapshot(),
			"gauges":   m.gaugeValues(),
		})
	}
}

func (m *Metrics) gaugeValues() map[string]float64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make(map[string]float64, len(m.gauges))
	for name, f := range m.gauges {
		out[name] = f()
	}
	return out
}

func (m *Metrics) writeProm(w http.ResponseWriter, s Snapshot) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	counters := []struct {
		name string
		val  int64
	}{
		{"railcache_hits_total", s.Hits}, {"railcache_stale_total", s.Stale},
		{"railcache_misses_total", s.Misses}, {"railcache_herd_suppressed_total", s.HerdSuppressed},
		{"railcache_fallbacks_total", s.Fallbacks}, {"railcache_db_fills_total", s.DBFills},
		{"railcache_refreshes_total", s.Refreshes}, {"railcache_errors_total", s.Errors},
		{"railcache_db_queries_total", s.DBQueries},
	}
	for _, c := range counters {
		fmt.Fprintf(w, "%s %d\n", c.name, c.val)
	}
	fmt.Fprintf(w, "railcache_hit_ratio %.4f\n", s.HitRatio)

	hs := m.latency.snapshot()
	for _, b := range hs.Buckets {
		fmt.Fprintf(w, "railcache_request_latency_ms_bucket{le=\"%g\"} %d\n", b.LE, b.Count)
	}
	fmt.Fprintf(w, "railcache_request_latency_ms_bucket{le=\"+Inf\"} %d\n", hs.Count)
	fmt.Fprintf(w, "railcache_request_latency_ms_sum %.3f\n", hs.Sum)
	fmt.Fprintf(w, "railcache_request_latency_ms_count %d\n", hs.Count)

	for _, name := range m.gaugeNamesSorted() {
		fmt.Fprintf(w, "railcache_%s %g\n", name, m.gaugeValue(name))
	}
}

func (m *Metrics) gaugeNamesSorted() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	names := make([]string, 0, len(m.gauges))
	for n := range m.gauges {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

func (m *Metrics) gaugeValue(name string) float64 {
	m.mu.RLock()
	f := m.gauges[name]
	m.mu.RUnlock()
	if f == nil {
		return 0
	}
	return f()
}

// histogram is a fixed-bucket cumulative latency histogram.
type histogram struct {
	les     []float64
	buckets []atomic.Int64
	count   atomic.Int64
	sumMs   atomicFloat
}

func newHistogram(les []float64) *histogram {
	return &histogram{les: les, buckets: make([]atomic.Int64, len(les))}
}

func (h *histogram) observe(ms float64) {
	h.count.Add(1)
	h.sumMs.add(ms)
	for i, le := range h.les {
		if ms <= le {
			h.buckets[i].Add(1)
		}
	}
}

// HistogramSnapshot is a serializable view.
type HistogramSnapshot struct {
	Buckets []BucketSnapshot `json:"buckets"`
	Count   int64            `json:"count"`
	Sum     float64          `json:"sum_ms"`
}

// BucketSnapshot is one cumulative bucket (count of observations <= LE).
type BucketSnapshot struct {
	LE    float64 `json:"le"`
	Count int64   `json:"count"`
}

func (h *histogram) snapshot() HistogramSnapshot {
	bs := make([]BucketSnapshot, len(h.les))
	for i, le := range h.les {
		bs[i] = BucketSnapshot{LE: le, Count: h.buckets[i].Load()}
	}
	return HistogramSnapshot{Buckets: bs, Count: h.count.Load(), Sum: h.sumMs.load()}
}

// atomicFloat is a tiny lock-free float64 accumulator.
type atomicFloat struct{ bits atomic.Uint64 }

func (a *atomicFloat) add(v float64) {
	for {
		old := a.bits.Load()
		newBits := math.Float64bits(math.Float64frombits(old) + v)
		if a.bits.CompareAndSwap(old, newBits) {
			return
		}
	}
}
func (a *atomicFloat) load() float64 { return math.Float64frombits(a.bits.Load()) }
