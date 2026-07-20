package search

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// StationLister supplies the set of valid station codes. Implemented by the
// store layer; defined here so validation depends on an interface, not on
// Postgres (keeps the import graph pointing inward and the validator testable).
type StationLister interface {
	ListStations(ctx context.Context) ([]string, error)
}

// StationCache holds the valid station-code set in process memory so request
// validation is an O(1) map lookup rather than a DB round trip. It is refreshed
// periodically in the background; a station set changes rarely, so brief
// staleness is fine and far cheaper than validating against Postgres per request.
type StationCache struct {
	lister StationLister
	log    *slog.Logger

	mu  sync.RWMutex
	set map[string]struct{}
}

// NewStationCache builds an empty cache. Call Refresh once at startup before
// serving so the whitelist is populated.
func NewStationCache(lister StationLister, log *slog.Logger) *StationCache {
	return &StationCache{lister: lister, log: log, set: map[string]struct{}{}}
}

// Refresh reloads the station set from the source. On error it keeps the
// previous set (fail-static): a transient DB blip must not empty the whitelist
// and start rejecting every valid query.
func (c *StationCache) Refresh(ctx context.Context) error {
	codes, err := c.lister.ListStations(ctx)
	if err != nil {
		return err
	}
	set := make(map[string]struct{}, len(codes))
	for _, code := range codes {
		set[code] = struct{}{}
	}
	c.mu.Lock()
	c.set = set
	c.mu.Unlock()
	return nil
}

// Has reports whether code is a known station.
func (c *StationCache) Has(code string) bool {
	c.mu.RLock()
	_, ok := c.set[code]
	c.mu.RUnlock()
	return ok
}

// Len returns the number of known stations (for health/observability).
func (c *StationCache) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.set)
}

// Run refreshes the cache every interval until ctx is cancelled. Intended to be
// launched in its own goroutine and drained at shutdown.
func (c *StationCache) Run(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := c.Refresh(ctx); err != nil {
				c.log.Warn("station cache refresh failed; keeping previous set", "err", err)
			}
		}
	}
}
