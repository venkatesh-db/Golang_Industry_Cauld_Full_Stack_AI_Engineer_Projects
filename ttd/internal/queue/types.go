package queue

import "time"

type Hold struct {
	ID          string    `json:"id"`
	Slot        string    `json:"slot"`
	VisitorID   string    `json:"visitor_id"`
	Status      string    `json:"status"`
	ExpiresAt   time.Time `json:"expires_at"`
	ConfirmedAt time.Time `json:"confirmed_at,omitempty"`
}

type Booking struct {
	ID        string    `json:"id"`
	HoldID    string    `json:"hold_id"`
	Slot      string    `json:"slot"`
	VisitorID string    `json:"visitor_id"`
	Confirmed time.Time `json:"confirmed_at"`
}

const (
	StatusHeld       = "held"
	StatusConfirming = "confirming"
	StatusConfirmed  = "confirmed"
	StatusExpired    = "expired"
	StatusCancelled  = "cancelled"
)
