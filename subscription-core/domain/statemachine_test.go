package domain

import (
	"errors"
	"testing"
)

func TestCanTransition_LegalPaths(t *testing.T) {
	legal := []struct{ from, to Status }{
		{StatusTrialing, StatusActive},
		{StatusTrialing, StatusPastDue},
		{StatusTrialing, StatusCanceled},
		{StatusTrialing, StatusPaused},
		{StatusActive, StatusPastDue},
		{StatusActive, StatusCanceled},
		{StatusActive, StatusPaused},
		{StatusPastDue, StatusActive}, // recovery after successful payment
		{StatusPastDue, StatusCanceled},
		{StatusPaused, StatusActive}, // resume
		{StatusPaused, StatusCanceled},
	}
	for _, c := range legal {
		if !CanTransition(c.from, c.to) {
			t.Errorf("expected legal transition %s -> %s", c.from, c.to)
		}
	}
}

func TestCanTransition_IllegalPaths(t *testing.T) {
	illegal := []struct{ from, to Status }{
		{StatusCanceled, StatusActive},  // terminal cannot resurrect
		{StatusCanceled, StatusPastDue}, // terminal
		{StatusCanceled, StatusPaused},  // terminal
		{StatusActive, StatusTrialing},  // cannot go back to trial
		{StatusPastDue, StatusPaused},   // not modeled
		{StatusPaused, StatusPastDue},   // not modeled
		{"bogus", StatusActive},         // unknown source
	}
	for _, c := range illegal {
		if CanTransition(c.from, c.to) {
			t.Errorf("expected ILLEGAL transition %s -> %s to be rejected", c.from, c.to)
		}
	}
}

func TestCanTransition_SameStateIsIdempotent(t *testing.T) {
	for _, s := range []Status{StatusTrialing, StatusActive, StatusPastDue, StatusCanceled, StatusPaused} {
		if !CanTransition(s, s) {
			t.Errorf("same-state transition %s -> %s must be an idempotent no-op", s, s)
		}
	}
}

func TestApplyTransition_LegalReturnsUpdatedCopy(t *testing.T) {
	sub := Subscription{ID: "sub_1", Status: StatusTrialing}
	got, err := ApplyTransition(sub, StatusActive)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Status != StatusActive {
		t.Errorf("status = %s, want %s", got.Status, StatusActive)
	}
	if sub.Status != StatusTrialing {
		t.Errorf("input mutated: sub.Status = %s, want %s (immutability violated)", sub.Status, StatusTrialing)
	}
}

func TestApplyTransition_IllegalReturnsError(t *testing.T) {
	sub := Subscription{ID: "sub_1", Status: StatusCanceled}
	got, err := ApplyTransition(sub, StatusActive)
	if !errors.Is(err, ErrIllegalTransition) {
		t.Fatalf("err = %v, want ErrIllegalTransition", err)
	}
	if got.Status != StatusCanceled {
		t.Errorf("on illegal transition status must be unchanged, got %s", got.Status)
	}
}
