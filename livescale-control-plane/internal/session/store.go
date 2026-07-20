// Package session tracks the sessionId -> (account, device) mapping with a TTL.
// Heartbeats extend the TTL; expiry drives device-count decrement in the
// concurrency manager. The interface lets the in-memory store (reference build)
// and a Redis store (real deployment) be swapped by config.
package session

import "errors"

// ErrNotFound is returned when a session is absent or already expired.
var ErrNotFound = errors.New("session: not found")

// Session is the stored value for a playback session.
type Session struct {
	AccountID string
	DeviceID  string
	AssetID   string
	ExpiresAt int64 // unix nanos
}

// Store is the session persistence boundary. Implementations must be safe for
// concurrent use.
type Store interface {
	// Put inserts a new session with the given absolute expiry (unix nanos).
	Put(sessionID string, s Session)
	// Extend refreshes expiry; returns the session and true if present.
	Extend(sessionID string, newExpiry int64) (Session, bool)
	// Get returns the session if present and not expired at nowNano.
	Get(sessionID string, nowNano int64) (Session, bool)
	// Delete removes a session (idempotent).
	Delete(sessionID string) (Session, bool)
}
