// Package search holds the RailCache read-path domain: query, results, and the
// cache-aside orchestration that shields Postgres behind Redis.
package search

import "time"

// Query is a normalized availability search request.
type Query struct {
	From  string // origin station code, uppercased
	To    string // destination station code, uppercased
	Date  string // travel date, YYYY-MM-DD
	Class string // coach class, e.g. 3A/2A/SL, uppercased
}

// Train is one row in a search result. Available < 0 is impossible; Available
// == 0 with a positive Total means the class exists but is waitlisted; a train
// present on the route with no availability row for the class surfaces with
// HasClass == false (LEFT JOIN) rather than vanishing from results.
type Train struct {
	Number    string `json:"number"`
	Name      string `json:"name"`
	DepFrom   string `json:"dep_from"` // departure time at origin
	ArrTo     string `json:"arr_to"`   // arrival time at destination
	Class     string `json:"class"`
	Available int    `json:"available"`
	Total     int    `json:"total"`
	HasClass  bool   `json:"has_class"` // false => class not offered / no inventory row
}

// SearchResult is the payload returned to a caller (and cached).
type SearchResult struct {
	Query  Query   `json:"query"`
	Trains []Train `json:"trains"`
}

// CacheEnvelope wraps a result for storage in Redis.
//
// Freshness is logical, not physical: the value is authoritative until
// FreshUntil, but is kept in Redis well past that (physical TTL = logical *
// multiplier) so it can be served stale-while-revalidate. Empty distinguishes a
// genuinely empty result (negative cache) from a cache miss.
type CacheEnvelope struct {
	Result     SearchResult `json:"result"`
	Empty      bool         `json:"empty"`
	CachedAt   time.Time    `json:"cached_at"`
	FreshUntil time.Time    `json:"fresh_until"`
}

// stale reports whether the envelope is past its logical freshness at t.
func (e CacheEnvelope) stale(t time.Time) bool { return !t.Before(e.FreshUntil) }

// CacheStatus is surfaced via the X-Cache header so the pattern is observable.
type CacheStatus string

const (
	StatusHit      CacheStatus = "HIT"      // fresh, straight from Redis
	StatusStale    CacheStatus = "STALE"    // served stale; background refresh triggered
	StatusMiss     CacheStatus = "MISS"     // cold fill from Postgres
	StatusFallback CacheStatus = "FALLBACK" // Redis unavailable; direct Postgres
)

// Outcome carries a result plus how it was served, for handlers and metrics.
type Outcome struct {
	Result     SearchResult
	Status     CacheStatus
	DBHit      bool          // whether this request touched Postgres
	DBTook     time.Duration // DB query duration when DBHit
	Suppressed bool          // request was a herd loser that reused another's fill
	AsOf       time.Time     // when the served data was read from Postgres (zero if unknown)
}
