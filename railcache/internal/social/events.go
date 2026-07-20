package social

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"
)

const (
	TopicEngagement   = "instagram.engagement.v1"
	TopicNotification = "instagram.notification.requested.v1"
	TopicDelivery     = "instagram.notification.delivered.v1"

	EventEngagementRecorded    = "instagram.engagement.recorded.v1"
	EventNotificationRequested = "instagram.notification.requested.v1"
	EventNotificationDelivered = "instagram.notification.delivered.v1"
)

// Event is the versioned envelope shared by every Kafka topic. Business data
// lives only in Data so routing, tracing, idempotency, and schema evolution are
// uniform across services.
type Event struct {
	ID            string          `json:"id"`
	Type          string          `json:"type"`
	SchemaVersion int             `json:"schema_version"`
	OccurredAt    time.Time       `json:"occurred_at"`
	Key           string          `json:"key"`
	CorrelationID string          `json:"correlation_id"`
	Data          json.RawMessage `json:"data"`
}

type LikeRecorded struct {
	LikeID       string `json:"like_id"`
	PostID       string `json:"post_id"`
	PostAuthorID string `json:"post_author_id"`
	ActorID      string `json:"actor_id"`
}

type NotificationRequested struct {
	NotificationID string `json:"notification_id"`
	RecipientID    string `json:"recipient_id"`
	ActorID        string `json:"actor_id"`
	PostID         string `json:"post_id"`
	Kind           string `json:"kind"`
	SourceEventID  string `json:"source_event_id"`
}

type NotificationDelivered struct {
	NotificationID string `json:"notification_id"`
	RecipientID    string `json:"recipient_id"`
	SourceEventID  string `json:"source_event_id"`
}

func NewEvent(kind, key, correlationID string, data any) (Event, error) {
	payload, err := json.Marshal(data)
	if err != nil {
		return Event{}, fmt.Errorf("marshal event payload: %w", err)
	}
	return Event{
		ID:            newID(),
		Type:          kind,
		SchemaVersion: 1,
		OccurredAt:    time.Now().UTC(),
		Key:           key,
		CorrelationID: correlationID,
		Data:          payload,
	}, nil
}

func (e Event) JSON() ([]byte, error) { return json.Marshal(e) }

func DecodeEvent(raw []byte) (Event, error) {
	var event Event
	if err := json.Unmarshal(raw, &event); err != nil {
		return Event{}, fmt.Errorf("decode event envelope: %w", err)
	}
	if event.ID == "" || event.Type == "" || event.Key == "" || event.SchemaVersion != 1 {
		return Event{}, fmt.Errorf("invalid event envelope")
	}
	return event, nil
}

func newID() string {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		// The time is only a last-resort process-local fallback. IDs are used for
		// idempotency, so a cryptographic random source is preferred in all normal
		// deployments.
		return fmt.Sprintf("evt-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(bytes[:])
}
