CREATE SCHEMA IF NOT EXISTS platform;
CREATE SCHEMA IF NOT EXISTS ride;
CREATE SCHEMA IF NOT EXISTS driver;
CREATE SCHEMA IF NOT EXISTS dispatch;
CREATE SCHEMA IF NOT EXISTS activity;

CREATE TABLE IF NOT EXISTS platform.schema_migrations (
    version TEXT PRIMARY KEY,
    applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS platform.outbox_events (
    id UUID PRIMARY KEY,
    topic TEXT NOT NULL,
    event_key TEXT NOT NULL,
    payload JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    published_at TIMESTAMPTZ,
    publish_attempts INTEGER NOT NULL DEFAULT 0,
    last_error TEXT
);
CREATE INDEX IF NOT EXISTS outbox_unpublished_idx ON platform.outbox_events (created_at) WHERE published_at IS NULL;

CREATE TABLE IF NOT EXISTS platform.processed_events (
    consumer_name TEXT NOT NULL,
    event_id UUID NOT NULL,
    processed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (consumer_name, event_id)
);

CREATE TABLE IF NOT EXISTS ride.rides (
    id UUID PRIMARY KEY,
    rider_name TEXT NOT NULL,
    pickup_latitude DOUBLE PRECISION NOT NULL,
    pickup_longitude DOUBLE PRECISION NOT NULL,
    destination TEXT NOT NULL,
    status TEXT NOT NULL,
    correlation_id UUID NOT NULL,
    requested_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS ride.ride_drivers (
    ride_id UUID PRIMARY KEY REFERENCES ride.rides(id),
    driver_id TEXT NOT NULL,
    driver_name TEXT NOT NULL,
    driver_phone TEXT NOT NULL,
    vehicle_label TEXT NOT NULL,
    color TEXT NOT NULL,
    assigned_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS driver.drivers (
    id TEXT PRIMARY KEY,
    display_name TEXT NOT NULL,
    phone TEXT NOT NULL,
    vehicle_label TEXT NOT NULL,
    color TEXT NOT NULL,
    latitude DOUBLE PRECISION NOT NULL,
    longitude DOUBLE PRECISION NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS dispatch.driver_availability (
    driver_id TEXT PRIMARY KEY,
    display_name TEXT NOT NULL,
    phone TEXT NOT NULL,
    vehicle_label TEXT NOT NULL,
    color TEXT NOT NULL,
    latitude DOUBLE PRECISION NOT NULL,
    longitude DOUBLE PRECISION NOT NULL,
    availability TEXT NOT NULL CHECK (availability IN ('AVAILABLE', 'BUSY')),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS dispatch.pending_rides (
    ride_id UUID PRIMARY KEY,
    rider_name TEXT NOT NULL,
    pickup_latitude DOUBLE PRECISION NOT NULL,
    pickup_longitude DOUBLE PRECISION NOT NULL,
    destination TEXT NOT NULL,
    requested_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS dispatch.assignments (
    id UUID PRIMARY KEY,
    ride_id UUID NOT NULL UNIQUE,
    driver_id TEXT NOT NULL,
    driver_name TEXT NOT NULL,
    driver_phone TEXT NOT NULL,
    vehicle_label TEXT NOT NULL,
    color TEXT NOT NULL,
    pickup_latitude DOUBLE PRECISION NOT NULL,
    pickup_longitude DOUBLE PRECISION NOT NULL,
    assigned_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS activity.ride_activity (
    event_id UUID PRIMARY KEY,
    ride_id UUID NOT NULL,
    kind TEXT NOT NULL,
    title TEXT NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL,
    payload JSONB NOT NULL,
    ingested_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS ride_activity_ride_time_idx ON activity.ride_activity (ride_id, occurred_at);

INSERT INTO driver.drivers (id, display_name, phone, vehicle_label, color, latitude, longitude)
VALUES
    ('driver-1', 'Aarav Kumar', '+91 98765 01001', 'KA 01 AB 1042 · Bike', '#f6b93b', 12.9716, 77.5946),
    ('driver-2', 'Priya Nair', '+91 98765 01002', 'KA 05 MQ 7310 · Auto', '#78e08f', 12.9750, 77.5990),
    ('driver-3', 'Rahul Shah', '+91 98765 01003', 'KA 03 KT 2211 · Bike', '#60a3bc', 12.9680, 77.5905),
    ('driver-4', 'Meera Iyer', '+91 98765 01004', 'KA 01 HR 8822 · Bike', '#e58e26', 12.9800, 77.5930),
    ('driver-5', 'Vikram Rao', '+91 98765 01005', 'KA 02 CW 3900 · Auto', '#b8e994', 12.9695, 77.6020),
    ('driver-6', 'Zoya Khan', '+91 98765 01006', 'KA 04 JD 6001 · Bike', '#82ccdd', 12.9730, 77.5870)
ON CONFLICT (id) DO NOTHING;

INSERT INTO dispatch.driver_availability (driver_id, display_name, phone, vehicle_label, color, latitude, longitude, availability)
SELECT id, display_name, phone, vehicle_label, color, latitude, longitude, 'AVAILABLE'
FROM driver.drivers
ON CONFLICT (driver_id) DO NOTHING;
