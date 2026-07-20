package ride

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/venkatesh/kafkaeda/internal/events"
	"github.com/venkatesh/kafkaeda/internal/platform/id"
	"github.com/venkatesh/kafkaeda/internal/platform/outbox"
	"github.com/venkatesh/kafkaeda/internal/platform/postgres"
)

const (
	StatusRequested = "REQUESTED"
	StatusSearching = "SEARCHING_DRIVER"
	StatusAssigned  = "DRIVER_ASSIGNED"
)

type CreateCommand struct {
	RiderName       string  `json:"rider_name"`
	PickupLatitude  float64 `json:"pickup_latitude"`
	PickupLongitude float64 `json:"pickup_longitude"`
	Destination     string  `json:"destination"`
}

type Service struct{ pool *pgxpool.Pool }

func NewService(pool *pgxpool.Pool) *Service { return &Service{pool: pool} }

func (s *Service) Create(ctx context.Context, command CreateCommand) (Ride, error) {
	command.RiderName = strings.TrimSpace(command.RiderName)
	command.Destination = strings.TrimSpace(command.Destination)
	if err := validate(command); err != nil {
		return Ride{}, err
	}
	rideID := id.New()
	correlationID := id.New()
	requestedAt := time.Now().UTC()
	created := Ride{
		ID: rideID, RiderName: command.RiderName, PickupLatitude: command.PickupLatitude,
		PickupLongitude: command.PickupLongitude, Destination: command.Destination,
		Status: StatusRequested, RequestedAt: requestedAt, UpdatedAt: requestedAt,
	}
	err := postgres.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO ride.rides (id, rider_name, pickup_latitude, pickup_longitude, destination, status, correlation_id, requested_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $8)`,
			rideID, command.RiderName, command.PickupLatitude, command.PickupLongitude,
			command.Destination, StatusRequested, correlationID, requestedAt)
		if err != nil {
			return fmt.Errorf("insert ride: %w", err)
		}
		event, err := events.New("ride.requested", "ride-api", correlationID, "", events.RideRequested{
			RideID: rideID, RiderName: command.RiderName, PickupLatitude: command.PickupLatitude,
			PickupLongitude: command.PickupLongitude, Destination: command.Destination,
		})
		if err != nil {
			return fmt.Errorf("create ride requested event: %w", err)
		}
		return outbox.Add(ctx, tx, events.RideRequestedTopic, rideID, event)
	})
	if err != nil {
		return Ride{}, err
	}
	return created, nil
}

func validate(command CreateCommand) error {
	command.RiderName = strings.TrimSpace(command.RiderName)
	command.Destination = strings.TrimSpace(command.Destination)
	if command.RiderName == "" || len(command.RiderName) > 80 {
		return fmt.Errorf("rider_name must contain 1 to 80 characters")
	}
	if command.Destination == "" || len(command.Destination) > 200 {
		return fmt.Errorf("destination must contain 1 to 200 characters")
	}
	if math.IsNaN(command.PickupLatitude) || command.PickupLatitude < -90 || command.PickupLatitude > 90 {
		return fmt.Errorf("pickup_latitude must be between -90 and 90")
	}
	if math.IsNaN(command.PickupLongitude) || command.PickupLongitude < -180 || command.PickupLongitude > 180 {
		return fmt.Errorf("pickup_longitude must be between -180 and 180")
	}
	return nil
}

type Ride struct {
	ID              string    `json:"id"`
	RiderName       string    `json:"rider_name"`
	PickupLatitude  float64   `json:"pickup_latitude"`
	PickupLongitude float64   `json:"pickup_longitude"`
	Destination     string    `json:"destination"`
	Status          string    `json:"status"`
	RequestedAt     time.Time `json:"requested_at"`
	UpdatedAt       time.Time `json:"updated_at"`
	Driver          *Driver   `json:"driver,omitempty"`
}

type Driver struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Phone        string `json:"phone"`
	VehicleLabel string `json:"vehicle_label"`
	Color        string `json:"color"`
}

func (s *Service) Get(ctx context.Context, rideID string) (Ride, error) {
	var item Ride
	var driver Driver
	var driverID, driverName, driverPhone, vehicleLabel, driverColor *string
	err := s.pool.QueryRow(ctx, `
		SELECT r.id::text, r.rider_name, r.pickup_latitude, r.pickup_longitude, r.destination,
		       r.status, r.requested_at, r.updated_at,
		       rd.driver_id, rd.driver_name, rd.driver_phone, rd.vehicle_label, rd.color
		FROM ride.rides r
		LEFT JOIN ride.ride_drivers rd ON rd.ride_id = r.id
		WHERE r.id = $1`, rideID).Scan(
		&item.ID, &item.RiderName, &item.PickupLatitude, &item.PickupLongitude, &item.Destination,
		&item.Status, &item.RequestedAt, &item.UpdatedAt,
		&driverID, &driverName, &driverPhone, &vehicleLabel, &driverColor)
	if err != nil {
		return Ride{}, err
	}
	if driverID != nil {
		driver.ID, driver.Name, driver.Phone, driver.VehicleLabel, driver.Color = *driverID, *driverName, *driverPhone, *vehicleLabel, *driverColor
		item.Driver = &driver
	}
	return item, nil
}

type Activity struct {
	EventID    string          `json:"event_id"`
	Kind       string          `json:"kind"`
	Title      string          `json:"title"`
	OccurredAt time.Time       `json:"occurred_at"`
	Payload    json.RawMessage `json:"payload"`
}

// Activity is a BFF read aggregation. It reads the Activity service's published
// read model but never mutates another service's state.
func (s *Service) Activity(ctx context.Context, rideID string) ([]Activity, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT event_id::text, kind, title, occurred_at, payload
		FROM activity.ride_activity
		WHERE ride_id = $1
		ORDER BY occurred_at, ingested_at`, rideID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]Activity, 0)
	for rows.Next() {
		var item Activity
		if err := rows.Scan(&item.EventID, &item.Kind, &item.Title, &item.OccurredAt, &item.Payload); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
