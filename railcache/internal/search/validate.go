package search

import (
	"fmt"
	"time"
)

// InvalidInputError describes why a query was rejected. Handlers map it to a 400
// so a malformed request is answered cheaply at the edge and never becomes a
// cache key or a DB query — this is the primary defense against cache-key
// cardinality attacks and uncacheable-error herds.
type InvalidInputError struct{ Reason string }

func (e *InvalidInputError) Error() string { return e.Reason }

// dateLayout is the sole accepted travel-date format.
const dateLayout = "2006-01-02"

// Validator enforces that a query is well-formed and references real reference
// data before it is allowed to reach the cache or the database.
type Validator struct {
	stations   *StationCache
	classes    map[string]struct{}
	windowDays int
	now        func() time.Time // injectable for tests
}

// NewValidator builds a validator. allowedClasses is the closed set of coach
// classes; windowDays bounds how far in the future a travel date may be.
func NewValidator(stations *StationCache, allowedClasses []string, windowDays int) *Validator {
	set := make(map[string]struct{}, len(allowedClasses))
	for _, c := range allowedClasses {
		set[c] = struct{}{}
	}
	return &Validator{stations: stations, classes: set, windowDays: windowDays, now: time.Now}
}

// Validate normalizes q and checks every field against reference data. It
// returns the normalized query and, on failure, an *InvalidInputError.
func (v *Validator) Validate(q Query) (Query, error) {
	q = q.Normalize()

	if q.From == q.To {
		return q, &InvalidInputError{Reason: "origin and destination must differ"}
	}
	if !v.stations.Has(q.From) {
		return q, &InvalidInputError{Reason: fmt.Sprintf("unknown origin station %q", q.From)}
	}
	if !v.stations.Has(q.To) {
		return q, &InvalidInputError{Reason: fmt.Sprintf("unknown destination station %q", q.To)}
	}
	if _, ok := v.classes[q.Class]; !ok {
		return q, &InvalidInputError{Reason: fmt.Sprintf("unsupported class %q", q.Class)}
	}

	day, err := time.Parse(dateLayout, q.Date)
	if err != nil {
		return q, &InvalidInputError{Reason: "date must be YYYY-MM-DD"}
	}
	today := v.now().UTC().Truncate(24 * time.Hour)
	day = day.UTC().Truncate(24 * time.Hour)
	if day.Before(today) {
		return q, &InvalidInputError{Reason: "travel date is in the past"}
	}
	if day.After(today.AddDate(0, 0, v.windowDays)) {
		return q, &InvalidInputError{Reason: fmt.Sprintf("travel date is beyond the %d-day booking window", v.windowDays)}
	}
	return q, nil
}
