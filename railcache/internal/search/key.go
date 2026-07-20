package search

import (
	"math/rand"
	"strings"
	"time"
)

// keyVersion lets us bust the entire cache namespace by bumping it.
const keyVersion = "v1"

// CacheKey builds the deterministic Redis key for a query. Normalization keeps
// hot keys colocated (same route/date/class → same key regardless of casing).
func CacheKey(q Query) string {
	return strings.Join([]string{
		"avail", keyVersion,
		strings.ToUpper(q.From),
		strings.ToUpper(q.To),
		q.Date,
		strings.ToUpper(q.Class),
	}, ":")
}

// LockKey is the fill-lock key paired with a cache key.
func LockKey(cacheKey string) string { return "lock:" + cacheKey }

// Normalize uppercases codes/class so equivalent queries share a key.
func (q Query) Normalize() Query {
	q.From = strings.ToUpper(strings.TrimSpace(q.From))
	q.To = strings.ToUpper(strings.TrimSpace(q.To))
	q.Class = strings.ToUpper(strings.TrimSpace(q.Class))
	q.Date = strings.TrimSpace(q.Date)
	return q
}

// jitterTTL applies +/- fraction jitter so many hot keys don't co-expire and
// synchronize a stampede. fraction is expected in [0,1).
func jitterTTL(base time.Duration, fraction float64) time.Duration {
	if fraction <= 0 {
		return base
	}
	delta := float64(base) * fraction
	// random offset in [-delta, +delta]
	off := (rand.Float64()*2 - 1) * delta
	return time.Duration(float64(base) + off)
}
