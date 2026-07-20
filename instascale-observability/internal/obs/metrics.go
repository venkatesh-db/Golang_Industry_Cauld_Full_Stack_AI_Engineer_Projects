// Package obs holds the shared observability plane: RED metrics, OTel tracing,
// structured logging, and the HTTP middleware/clients that wire trace_id through
// every signal. All three services import this package so telemetry is uniform.
package obs

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
)

// Metrics holds the RED (Rate/Errors/Duration) instruments plus runtime/pool gauges.
// Duration is a classic histogram that emits exemplars carrying the active trace_id,
// which is what lets Grafana jump metric -> trace in one click.
type Metrics struct {
	Registry   *prometheus.Registry
	Requests   *prometheus.CounterVec
	Duration   *prometheus.HistogramVec
	InFlight   *prometheus.GaugeVec
	Goroutines prometheus.GaugeFunc // wired by the runtime collector below

	// DB pool gauges — updated from pgxpool.Stat() so DB-pool-exhaustion chaos is visible.
	DBPoolAcquired  prometheus.Gauge
	DBPoolIdle      prometheus.Gauge
	DBPoolMax       prometheus.Gauge
	DBPoolWaitCount prometheus.Gauge
}

// NewMetrics builds a private registry (no global default) so each binary owns its
// exposition and tests stay isolated. service label is baked into every series.
func NewMetrics(service string) *Metrics {
	reg := prometheus.NewRegistry()
	constLabels := prometheus.Labels{"service": service}

	m := &Metrics{
		Registry: reg,
		Requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "http_requests_total",
			Help:        "Total HTTP requests by route/method/code.",
			ConstLabels: constLabels,
		}, []string{"route", "method", "code"}),
		Duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:        "http_request_duration_seconds",
			Help:        "HTTP request latency; exemplars carry trace_id.",
			ConstLabels: constLabels,
			// Buckets tuned for a p95/p99 SLO story around ~50ms-1s.
			Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5},
		}, []string{"route", "method"}),
		InFlight: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "http_inflight_requests",
			Help:        "In-flight HTTP requests.",
			ConstLabels: constLabels,
		}, []string{"route"}),
		DBPoolAcquired:  poolGauge("db_pool_acquired_conns", "Currently acquired pgx conns.", constLabels),
		DBPoolIdle:      poolGauge("db_pool_idle_conns", "Idle pgx conns.", constLabels),
		DBPoolMax:       poolGauge("db_pool_max_conns", "Max pgx conns.", constLabels),
		DBPoolWaitCount: poolGauge("db_pool_wait_count", "Cumulative pool acquire waits.", constLabels),
	}

	reg.MustRegister(m.Requests, m.Duration, m.InFlight,
		m.DBPoolAcquired, m.DBPoolIdle, m.DBPoolMax, m.DBPoolWaitCount)
	// Go runtime + process collectors give us goroutines, GC pause, RSS —
	// the signatures for goroutine-leak and memory-pressure chaos. Wrap the
	// registerer so these built-in metrics ALSO carry the service label; the
	// dashboard filters everything by {service=~"$service"}, so without this the
	// go_* / process_* panels show "No data".
	svcReg := prometheus.WrapRegistererWith(constLabels, reg)
	svcReg.MustRegister(collectors.NewGoCollector())
	svcReg.MustRegister(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	return m
}

func poolGauge(name, help string, labels prometheus.Labels) prometheus.Gauge {
	return prometheus.NewGauge(prometheus.GaugeOpts{Name: name, Help: help, ConstLabels: labels})
}
