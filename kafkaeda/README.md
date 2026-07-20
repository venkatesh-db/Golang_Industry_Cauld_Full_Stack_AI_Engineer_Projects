# Rapido Dispatch — Go + Kafka event-driven reference build

This project implements the most valuable Rapido-style capability: **request a ride and dispatch the nearest available driver**. The browser UI is intentionally small; the focus is a production-shaped event flow through independently deployable services.

## Run it

```bash
docker compose up --build
```

Open [http://localhost:8091](http://localhost:8091). Six nearby drivers are seeded by the migration, so a ride is assigned in a few seconds. The Driver API is available on port `8081` for location updates.

```bash
curl -X POST http://localhost:8081/api/drivers/driver-1/location \
  -H 'content-type: application/json' \
  -d '{"latitude":12.973,"longitude":77.596}'
```

## Event flow

```text
Browser -> ride-api -> Postgres (ride + outbox row)
                         |
                   outbox-relay -> Kafka: ride.requested.v1
                                         |
                                  dispatch-service
                         Postgres (assignment + driver lease + outbox row)
                                         |
                   outbox-relay -> Kafka: ride.assigned.v1
                                         |                 |
                                ride-projection     activity-service
                               (ride state view)   (immutable timeline)
                                         |
                        Kafka: ride.status.changed.v1 -> activity-service
```

`driver.location.updated.v1` follows the same transactional-outbox path from Driver API to Dispatch, keeping the dispatch projection current.

## Design commitments

- **Transactional outbox**: no domain event is emitted before its database transaction commits.
- **At-least-once Kafka delivery with idempotent consumers**: each service records event IDs in `platform.processed_events` in the same transaction as its state change.
- **Competing dispatch safety**: candidate selection uses `FOR UPDATE SKIP LOCKED`; a driver can receive one active assignment only.
- **Explicit event contracts**: JSON envelopes carry event ID, correlation ID, causal event ID, schema version, producer and occurrence time.
- **Read models instead of cross-service joins**: Ride Projection persists driver details from the `ride.assigned` event; Activity persists its own timeline.

## Local topology

| Process | Responsibility |
| --- | --- |
| `ride-api` | Creates rides and serves the simple UI/read model. |
| `driver-api` | Receives driver location pings. |
| `outbox-relay` | Publishes committed outbox rows to Kafka. |
| `dispatch-service` | Maintains available-driver projection and reserves a driver. |
| `ride-projection` | Applies assignment events to the ride read/write model. |
| `activity-service` | Builds an append-only rider activity timeline. |

For a real deployment, database ownership would be separated per service, Kafka ACLs would restrict topic access, events would use Avro/Protobuf with Schema Registry, and the relay would use a lease/outbox partitioning strategy or CDC.
