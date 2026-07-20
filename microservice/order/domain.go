package order

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

type Side string

const (
	Buy  Side = "BUY"
	Sell Side = "SELL"
)

type OrderStatus string

const (
	PendingMargin          OrderStatus = "PENDING_MARGIN"
	PendingRoute           OrderStatus = "PENDING_ROUTE"
	Accepted               OrderStatus = "ACCEPTED"
	Rejected               OrderStatus = "REJECTED"
	ReconciliationRequired OrderStatus = "RECONCILIATION_REQUIRED"
)

type MarginState string

const (
	MarginNone           MarginState = "NONE"
	MarginReserved       MarginState = "RESERVED"
	MarginReleasePending MarginState = "RELEASE_PENDING"
	MarginReleased       MarginState = "RELEASED"
)

type EventKind string

const (
	ReserveMargin EventKind = "margin.reserve.v1"
	RouteExchange EventKind = "exchange.route.v1"
	ReleaseMargin EventKind = "margin.release.v1"
)

type EventState string

const (
	EventPending    EventState = "PENDING"
	EventRetry      EventState = "RETRY"
	EventProcessing EventState = "PROCESSING"
	EventDelivered  EventState = "DELIVERED"
	EventDead       EventState = "DEAD"
)

type Order struct {
	ID              string
	AccountID       string
	IdempotencyKey  string
	RequestHash     string
	Instrument      string
	Side            Side
	Quantity        int64
	LimitPrice      int64 // paise; integer money avoids float errors
	Status          OrderStatus
	Margin          MarginState
	ReservationID   string
	ExchangeOrderID string
	FailureReason   string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type OutboxEvent struct {
	ID          string
	OrderID     string
	Kind        EventKind
	State       EventState
	Attempts    int
	AvailableAt time.Time
	LeaseToken  string
	LeaseUntil  time.Time
	LastError   string
	CreatedAt   time.Time
	DeliveredAt time.Time
}

func newID() (string, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("generate ID: %w", err)
	}
	return hex.EncodeToString(bytes), nil
}

func idempotencyIndex(accountID, key string) string { return accountID + "\x00" + key }
