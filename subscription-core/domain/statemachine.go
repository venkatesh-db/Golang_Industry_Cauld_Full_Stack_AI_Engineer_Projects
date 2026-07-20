package domain

import (
	"errors"
	"fmt"
)

// ErrIllegalTransition is returned when a transition is not in the table.
var ErrIllegalTransition = errors.New("illegal subscription state transition")

// transitions is the declared, exhaustive transition table. Any target not
// listed for a source state is rejected. Canceled is terminal.
var transitions = map[Status]map[Status]bool{
	StatusTrialing: {StatusActive: true, StatusPastDue: true, StatusCanceled: true, StatusPaused: true},
	StatusActive:   {StatusPastDue: true, StatusCanceled: true, StatusPaused: true},
	StatusPastDue:  {StatusActive: true, StatusCanceled: true},
	StatusPaused:   {StatusActive: true, StatusCanceled: true},
	StatusCanceled: {}, // terminal
}

// CanTransition reports whether from->to is legal. Same-state is an idempotent no-op.
func CanTransition(from, to Status) bool {
	if from == to {
		return true
	}
	targets, ok := transitions[from]
	if !ok {
		return false
	}
	return targets[to]
}

// ApplyTransition returns a copy of s with Status set to `to`, or an error if
// the transition is illegal. The input is never mutated (immutable update).
func ApplyTransition(s Subscription, to Status) (Subscription, error) {
	if !CanTransition(s.Status, to) {
		return s, fmt.Errorf("%w: %s -> %s", ErrIllegalTransition, s.Status, to)
	}
	s.Status = to
	return s, nil
}
