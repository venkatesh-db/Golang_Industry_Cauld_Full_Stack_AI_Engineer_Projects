package events

import (
	"encoding/json"
	"time"

	"github.com/venkatesh/kafkaeda/internal/platform/id"
)

const (
	RideRequestedTopic        = "ride.requested.v1"
	DriverLocationTopic       = "driver.location.updated.v1"
	RideAssignedTopic         = "ride.assigned.v1"
	RideDispatchDeferredTopic = "ride.dispatch.deferred.v1"
	RideStatusChangedTopic    = "ride.status.changed.v1"
)

type Envelope struct {
	EventID       string          `json:"event_id"`
	EventType     string          `json:"event_type"`
	SchemaVersion int             `json:"schema_version"`
	OccurredAt    time.Time       `json:"occurred_at"`
	Producer      string          `json:"producer"`
	CorrelationID string          `json:"correlation_id"`
	CausationID   string          `json:"causation_id,omitempty"`
	Data          json.RawMessage `json:"data"`
}

func New(eventType, producer, correlationID, causationID string, data any) (Envelope, error) {
	payload, err := json.Marshal(data)
	if err != nil {
		return Envelope{}, err
	}
	return Envelope{
		EventID:       id.New(),
		EventType:     eventType,
		SchemaVersion: 1,
		OccurredAt:    time.Now().UTC(),
		Producer:      producer,
		CorrelationID: correlationID,
		CausationID:   causationID,
		Data:          payload,
	}, nil
}

type RideRequested struct {
	RideID          string  `json:"ride_id"`
	RiderName       string  `json:"rider_name"`
	PickupLatitude  float64 `json:"pickup_latitude"`
	PickupLongitude float64 `json:"pickup_longitude"`
	Destination     string  `json:"destination"`
}

type DriverLocationUpdated struct {
	DriverID  string  `json:"driver_id"`
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
}

type RideAssigned struct {
	RideID       string `json:"ride_id"`
	AssignmentID string `json:"assignment_id"`
	DriverID     string `json:"driver_id"`
	DriverName   string `json:"driver_name"`
	DriverPhone  string `json:"driver_phone"`
	VehicleLabel string `json:"vehicle_label"`
	Color        string `json:"color"`
}

type RideDispatchDeferred struct {
	RideID string `json:"ride_id"`
	Reason string `json:"reason"`
}

type RideStatusChanged struct {
	RideID   string `json:"ride_id"`
	Previous string `json:"previous_status"`
	Current  string `json:"current_status"`
	DriverID string `json:"driver_id,omitempty"`
}
