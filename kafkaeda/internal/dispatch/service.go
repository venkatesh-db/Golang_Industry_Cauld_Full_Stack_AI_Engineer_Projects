package dispatch

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/venkatesh/kafkaeda/internal/events"
	"github.com/venkatesh/kafkaeda/internal/platform/consumer"
	"github.com/venkatesh/kafkaeda/internal/platform/id"
	"github.com/venkatesh/kafkaeda/internal/platform/outbox"
)

func Run(ctx context.Context, pool *pgxpool.Pool, brokers []string) error {
	return runBoth(ctx,
		func() error {
			return consumer.Run(ctx, pool, brokers, consumer.Config{
				Topic: events.RideRequestedTopic, GroupID: "dispatch-requested-v1", ConsumerName: "dispatch-service.requested-v1",
			}, handleRideRequested)
		},
		func() error {
			return consumer.Run(ctx, pool, brokers, consumer.Config{
				Topic: events.DriverLocationTopic, GroupID: "dispatch-location-v1", ConsumerName: "dispatch-service.location-v1",
			}, handleDriverLocation)
		},
	)
}

func handleRideRequested(ctx context.Context, tx pgx.Tx, envelope events.Envelope) error {
	var data events.RideRequested
	if err := json.Unmarshal(envelope.Data, &data); err != nil {
		return fmt.Errorf("decode ride requested: %w", err)
	}
	inserted, err := tx.Exec(ctx, `
		INSERT INTO dispatch.pending_rides (ride_id, rider_name, pickup_latitude, pickup_longitude, destination)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (ride_id) DO NOTHING`, data.RideID, data.RiderName, data.PickupLatitude, data.PickupLongitude, data.Destination)
	if err != nil {
		return fmt.Errorf("record pending ride: %w", err)
	}
	if inserted.RowsAffected() == 0 {
		return nil
	}
	matched, err := reserveDriver(ctx, tx, envelope, data.RideID, data.PickupLatitude, data.PickupLongitude)
	if err != nil {
		return err
	}
	if !matched {
		deferred, err := events.New("ride.dispatch.deferred", "dispatch-service", envelope.CorrelationID, envelope.EventID, events.RideDispatchDeferred{
			RideID: data.RideID, Reason: "no driver currently available",
		})
		if err != nil {
			return err
		}
		return outbox.Add(ctx, tx, events.RideDispatchDeferredTopic, data.RideID, deferred)
	}
	return nil
}

func handleDriverLocation(ctx context.Context, tx pgx.Tx, envelope events.Envelope) error {
	var data events.DriverLocationUpdated
	if err := json.Unmarshal(envelope.Data, &data); err != nil {
		return fmt.Errorf("decode driver location: %w", err)
	}
	// This is the Dispatch service's own local projection. A location ping must
	// never turn a reserved driver back to AVAILABLE.
	_, err := tx.Exec(ctx, `
		UPDATE dispatch.driver_availability SET latitude = $2, longitude = $3, updated_at = now()
		WHERE driver_id = $1`, data.DriverID, data.Latitude, data.Longitude)
	if err != nil {
		return fmt.Errorf("update dispatch location projection: %w", err)
	}
	return matchOldestPending(ctx, tx, envelope)
}

func matchOldestPending(ctx context.Context, tx pgx.Tx, envelope events.Envelope) error {
	var rideID string
	var latitude, longitude float64
	err := tx.QueryRow(ctx, `
		SELECT ride_id::text, pickup_latitude, pickup_longitude
		FROM dispatch.pending_rides
		ORDER BY requested_at
		LIMIT 1
		FOR UPDATE SKIP LOCKED`).Scan(&rideID, &latitude, &longitude)
	if err == pgx.ErrNoRows {
		return nil
	}
	if err != nil {
		return fmt.Errorf("select pending ride: %w", err)
	}
	_, err = reserveDriver(ctx, tx, envelope, rideID, latitude, longitude)
	return err
}

type candidate struct {
	id, name, phone, vehicle, color string
}

func reserveDriver(ctx context.Context, tx pgx.Tx, envelope events.Envelope, rideID string, latitude, longitude float64) (bool, error) {
	var driver candidate
	err := tx.QueryRow(ctx, `
		SELECT driver_id, display_name, phone, vehicle_label, color
		FROM dispatch.driver_availability
		WHERE availability = 'AVAILABLE'
		ORDER BY ((latitude - $1) * (latitude - $1)) + ((longitude - $2) * (longitude - $2)), updated_at
		LIMIT 1
		FOR UPDATE SKIP LOCKED`, latitude, longitude).Scan(&driver.id, &driver.name, &driver.phone, &driver.vehicle, &driver.color)
	if err == pgx.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("select available driver: %w", err)
	}
	result, err := tx.Exec(ctx, `UPDATE dispatch.driver_availability
		SET availability = 'BUSY', updated_at = now()
		WHERE driver_id = $1 AND availability = 'AVAILABLE'`, driver.id)
	if err != nil {
		return false, fmt.Errorf("reserve driver: %w", err)
	}
	if result.RowsAffected() != 1 {
		return false, nil
	}
	assignmentID := id.New()
	_, err = tx.Exec(ctx, `
		INSERT INTO dispatch.assignments (id, ride_id, driver_id, driver_name, driver_phone, vehicle_label, color, pickup_latitude, pickup_longitude)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`, assignmentID, rideID, driver.id, driver.name, driver.phone, driver.vehicle, driver.color, latitude, longitude)
	if err != nil {
		return false, fmt.Errorf("create assignment: %w", err)
	}
	_, err = tx.Exec(ctx, `DELETE FROM dispatch.pending_rides WHERE ride_id = $1`, rideID)
	if err != nil {
		return false, fmt.Errorf("remove pending ride: %w", err)
	}
	assigned, err := events.New("ride.assigned", "dispatch-service", envelope.CorrelationID, envelope.EventID, events.RideAssigned{
		RideID: rideID, AssignmentID: assignmentID, DriverID: driver.id, DriverName: driver.name,
		DriverPhone: driver.phone, VehicleLabel: driver.vehicle, Color: driver.color,
	})
	if err != nil {
		return false, err
	}
	if err := outbox.Add(ctx, tx, events.RideAssignedTopic, rideID, assigned); err != nil {
		return false, err
	}
	return true, nil
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
