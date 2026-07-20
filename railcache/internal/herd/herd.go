// Package herd is a built-in concurrency load generator that proves the cache
// collapses a thundering herd to (ideally) one DB fill.
package herd

import (
	"context"
	"sort"
	"sync"
	"time"

	"railcache/internal/search"
)

// DoFunc runs one search request.
type DoFunc func(ctx context.Context) (search.Outcome, error)

// Report summarizes a herd run.
type Report struct {
	N           int            `json:"n"`
	Concurrency int            `json:"concurrency"`
	DurationMs  int64          `json:"duration_ms"`
	P50Ms       float64        `json:"p50_ms"`
	P95Ms       float64        `json:"p95_ms"`
	P99Ms       float64        `json:"p99_ms"`
	Statuses    map[string]int `json:"statuses"`
	Errors      int            `json:"errors"`
}

// Run fires n requests through do with the given concurrency and returns timing
// and status distribution. The caller pairs this with a metrics snapshot delta
// to report exact DB fills.
func Run(ctx context.Context, n, concurrency int, do DoFunc) Report {
	if concurrency <= 0 {
		concurrency = n
	}
	latencies := make([]time.Duration, n)
	statuses := make([]search.CacheStatus, n)
	errs := make([]bool, n)

	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	start := time.Now()
	for i := 0; i < n; i++ {
		wg.Add(1)
		sem <- struct{}{}
		go func(idx int) {
			defer wg.Done()
			defer func() { <-sem }()
			t0 := time.Now()
			out, err := do(ctx)
			latencies[idx] = time.Since(t0)
			if err != nil {
				errs[idx] = true
				return
			}
			statuses[idx] = out.Status
		}(i)
	}
	wg.Wait()
	total := time.Since(start)

	statusCounts := map[string]int{}
	errCount := 0
	for i := 0; i < n; i++ {
		if errs[i] {
			errCount++
			continue
		}
		statusCounts[string(statuses[i])]++
	}

	return Report{
		N: n, Concurrency: concurrency, DurationMs: total.Milliseconds(),
		P50Ms: pct(latencies, 50), P95Ms: pct(latencies, 95), P99Ms: pct(latencies, 99),
		Statuses: statusCounts, Errors: errCount,
	}
}

func pct(ds []time.Duration, p int) float64 {
	if len(ds) == 0 {
		return 0
	}
	cp := make([]time.Duration, len(ds))
	copy(cp, ds)
	sort.Slice(cp, func(i, j int) bool { return cp[i] < cp[j] })
	idx := (p * (len(cp) - 1)) / 100
	return float64(cp[idx].Microseconds()) / 1000.0
}
