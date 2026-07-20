package search

import "time"

// freshnessFor returns the logical TTL for a positive result, as a function of
// how close the travel date is. Availability churns fastest in the Tatkal
// window (today / tomorrow), where a stale "AVAILABLE" is the single worst UX in
// rail booking ("search said available, booking failed"). Far-future dates
// change slowly and can be cached far longer. This is policy, not a global
// constant — freshness is a property of the query, not the system.
func (p Params) freshnessFor(q Query) time.Duration {
	switch d := daysAhead(q.Date); {
	case d < 0:
		return p.TTL // unparseable (shouldn't happen post-validation) → conservative base
	case d <= 1:
		return p.TatkalTTL // today/tomorrow: seconds
	case d <= 7:
		return p.TTL / 2
	default:
		return p.TTL
	}
}

// daysAhead parses a YYYY-MM-DD date and returns whole days from today (UTC),
// or -1 if it cannot be parsed.
func daysAhead(date string) int {
	day, err := time.Parse(dateLayout, date)
	if err != nil {
		return -1
	}
	today := time.Now().UTC().Truncate(24 * time.Hour)
	return int(day.UTC().Truncate(24*time.Hour).Sub(today).Hours() / 24)
}
