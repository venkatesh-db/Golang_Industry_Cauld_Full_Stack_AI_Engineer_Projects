-- RailCache schema: Postgres is the source of truth for routes and inventory.

CREATE TABLE IF NOT EXISTS stations (
    code TEXT PRIMARY KEY,
    name TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS trains (
    id          INT PRIMARY KEY,
    number      TEXT NOT NULL UNIQUE,
    name        TEXT NOT NULL,
    source_code TEXT NOT NULL REFERENCES stations(code),
    dest_code   TEXT NOT NULL REFERENCES stations(code)
);

-- Ordered stops along a train's route. Route match: origin.seq < destination.seq.
CREATE TABLE IF NOT EXISTS train_stops (
    train_id     INT  NOT NULL REFERENCES trains(id),
    station_code TEXT NOT NULL REFERENCES stations(code),
    seq          INT  NOT NULL,
    arr          TEXT,            -- HH:MM local, stored as text for demo simplicity
    dep          TEXT,
    day_offset   INT  NOT NULL DEFAULT 0,
    PRIMARY KEY (train_id, seq)
);

CREATE INDEX IF NOT EXISTS idx_train_stops_station ON train_stops(station_code);

-- Mutable seat inventory per train/date/class — the hot, changing data.
CREATE TABLE IF NOT EXISTS seat_availability (
    train_id    INT  NOT NULL REFERENCES trains(id),
    travel_date DATE NOT NULL,
    class       TEXT NOT NULL,
    total       INT  NOT NULL,
    available   INT  NOT NULL,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (train_id, travel_date, class)
);
