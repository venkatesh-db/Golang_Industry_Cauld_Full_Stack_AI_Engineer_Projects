package ride

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/venkatesh/kafkaeda/internal/events"
	"github.com/venkatesh/kafkaeda/internal/platform/consumer"
	"github.com/venkatesh/kafkaeda/internal/platform/outbox"
)

func RunProjector(ctx context.Context, pool *pgxpool.Pool, brokers []string) error {
	return runBoth(ctx,
		func() error {
			return consumer.Run(ctx, pool, brokers, consumer.Config{
				Topic: events.RideAssignedTopic, GroupID: "ride-projection-assigned-v1", ConsumerName: "ride-projection.assigned-v1",
			}, applyAssigned)
		},
		func() error {
			return consumer.Run(ctx, pool, brokers, consumer.Config{
				Topic: events.RideDispatchDeferredTopic, GroupID: "ride-projection-deferred-v1", ConsumerName: "ride-projection.deferred-v1",
			}, applyDeferred)
		},
	)
}

func applyAssigned(ctx context.Context, tx pgx.Tx, envelope events.Envelope) error {
	var data events.RideAssigned
	if err := json.Unmarshal(envelope.Data, &data); err != nil {
		return fmt.Errorf("decode ride assigned: %w", err)
	}
	result, err := tx.Exec(ctx, `
		UPDATE ride.rides SET status = $2, updated_at = now()
		WHERE id = $1 AND status <> $2`, data.RideID, StatusAssigned)
	if err != nil {
		return fmt.Errorf("mark ride assigned: %w", err)
	}
	if result.RowsAffected() == 0 {
		return nil
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO ride.ride_drivers (ride_id, driver_id, driver_name, driver_phone, vehicle_label, color)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (ride_id) DO UPDATE SET driver_id = EXCLUDED.driver_id, driver_name = EXCLUDED.driver_name,
			driver_phone = EXCLUDED.driver_phone, vehicle_label = EXCLUDED.vehicle_label, color = EXCLUDED.color`,
		data.RideID, data.DriverID, data.DriverName, data.DriverPhone, data.VehicleLabel, data.Color)
	if err != nil {
		return fmt.Errorf("project assigned driver: %w", err)
	}
	statusEvent, err := events.New("ride.status.changed", "ride-projection", envelope.CorrelationID, envelope.EventID, events.RideStatusChanged{
		RideID: data.RideID, Previous: StatusRequested, Current: StatusAssigned, DriverID: data.DriverID,
	})
	if err != nil {
		return err
	}
	return outbox.Add(ctx, tx, events.RideStatusChangedTopic, data.RideID, statusEvent)
}

func applyDeferred(ctx context.Context, tx pgx.Tx, envelope events.Envelope) error {
	var data events.RideDispatchDeferred
	if err := json.Unmarshal(envelope.Data, &data); err != nil {
		return fmt.Errorf("decode dispatch deferred: %w", err)
	}
	result, err := tx.Exec(ctx, `
		UPDATE ride.rides SET status = $2, updated_at = now()
		WHERE id = $1 AND status = $3`, data.RideID, StatusSearching, StatusRequested)
	if err != nil {
		return fmt.Errorf("project deferred dispatch: %w", err)
	}
	if result.RowsAffected() == 0 {
		return nil
	}
	statusEvent, err := events.New("ride.status.changed", "ride-projection", envelope.CorrelationID, envelope.EventID, events.RideStatusChanged{
		RideID: data.RideID, Previous: StatusRequested, Current: StatusSearching,
	})
	if err != nil {
		return err
	}
	return outbox.Add(ctx, tx, events.RideStatusChangedTopic, data.RideID, statusEvent)
}

func runBoth(ctx context.Context, first, second func() error) error {
	errors := make(chan error, 2)
	go func() { errors <- first() }()
	go func() { errors <- second() }()
	select {
	case <-ctx.Done():
		return nil
	case err := <-errors:
		return err
	}
}
