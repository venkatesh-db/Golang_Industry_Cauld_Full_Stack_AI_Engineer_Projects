package activity

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/venkatesh/kafkaeda/internal/events"
	"github.com/venkatesh/kafkaeda/internal/platform/consumer"
)

func Run(ctx context.Context, pool *pgxpool.Pool, brokers []string) error {
	configs := []consumer.Config{
		{Topic: events.RideRequestedTopic, GroupID: "activity-requested-v1", ConsumerName: "activity-service.requested-v1"},
		{Topic: events.RideDispatchDeferredTopic, GroupID: "activity-deferred-v1", ConsumerName: "activity-service.deferred-v1"},
		{Topic: events.RideAssignedTopic, GroupID: "activity-assigned-v1", ConsumerName: "activity-service.assigned-v1"},
		{Topic: events.RideStatusChangedTopic, GroupID: "activity-status-v1", ConsumerName: "activity-service.status-v1"},
	}
	errors := make(chan error, len(configs))
	for _, item := range configs {
		config := item
		go func() { errors <- consumer.Run(ctx, pool, brokers, config, record) }()
	}
	select {
	case <-ctx.Done():
		return nil
	case err := <-errors:
		return err
	}
}

func record(ctx context.Context, tx pgx.Tx, envelope events.Envelope) error {
	rideID, kind, title, err := details(envelope)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO activity.ride_activity (event_id, ride_id, kind, title, occurred_at, payload)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (event_id) DO NOTHING`, envelope.EventID, rideID, kind, title, envelope.OccurredAt, envelope.Data)
	if err != nil {
		return fmt.Errorf("record ride activity: %w", err)
	}
	return nil
}

func details(envelope events.Envelope) (rideID, kind, title string, err error) {
	switch envelope.EventType {
	case "ride.requested":
		var data events.RideRequested
		err = json.Unmarshal(envelope.Data, &data)
		return data.RideID, "requested", "Ride request received", err
	case "ride.dispatch.deferred":
		var data events.RideDispatchDeferred
		err = json.Unmarshal(envelope.Data, &data)
		return data.RideID, "dispatch_deferred", "Looking for an available captain", err
	case "ride.assigned":
		var data events.RideAssigned
		err = json.Unmarshal(envelope.Data, &data)
		return data.RideID, "assigned", "Captain " + data.DriverName + " is assigned", err
	case "ride.status.changed":
		var data events.RideStatusChanged
		err = json.Unmarshal(envelope.Data, &data)
		return data.RideID, "status_changed", "Ride status: " + data.Current, err
	default:
		return "", "", "", fmt.Errorf("unexpected activity event type %q", envelope.EventType)
	}
}
