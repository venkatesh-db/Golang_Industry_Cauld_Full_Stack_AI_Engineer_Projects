# Food Delivery — Event-Driven Architecture (Swiggy/Zomato-style)

An order is a **state machine driven by events**. No service calls another service
directly — they publish and react to *facts* on a message bus. This is the shape
real food-delivery backends use, distilled into a runnable, zero-dependency demo.

```
POST /orders
     │  order.placed
     ▼
┌──────────────┐ payment.requested ┌──────────────┐
│ OrderService │ ────────────────▶ │   Payment    │  (flaky gateway: retries + declines)
│  (aggregate  │ ◀──────────────── │              │
│   + projector)│  payment.authorized/failed        │
└──────┬───────┘                   └──────────────┘
       │ (restaurant reacts to payment.authorized)
       ▼
┌──────────────┐ restaurant.accepted ┌──────────────┐
│  Restaurant  │ ─────────┬────────▶ │   Delivery   │  assigns rider (retries if none free)
│  + Kitchen   │          │          │              │  food.ready → picked_up → delivered
└──────────────┘   food.ready ─────▶ └──────────────┘

        NotificationService reacts to everything (pure consumer)
```

## Run it

```bash
npm install
npm run sim     # fire a burst of orders, watch the streams interleave
npm start       # HTTP API on :3000
```

```bash
curl -XPOST localhost:3000/orders -d '{"customerId":"venkatesh","amount":499}'
curl localhost:3000/orders/<id>     # full event timeline
curl -N localhost:3000/stream        # live SSE feed
```

## The EDA concepts this demonstrates

| Concept | Where |
|---|---|
| **Broker abstraction** (swap in Kafka/RabbitMQ) | `src/bus/EventBus.ts` |
| **Consumer groups + fan-out** | `subscribe(topic, group, …)` in `InMemoryBus` |
| **At-least-once + retry with backoff** | `InMemoryBus.deliver()` |
| **Dead-letter queue** (poison messages) | `InMemoryBus` → `☠️ DLQ` after 4 attempts |
| **Idempotent consumers** (exactly-once *effect*) | `processed` set per group |
| **Event sourcing / replayable log** | `InMemoryBus.log` |
| **Materialized read model (CQRS)** | `src/store/store.ts` — a projection of the stream |
| **State machine** | `src/domain/orderStateMachine.ts` |
| **Failure branches** | declined payment, restaurant rejection, no rider |
| **Async command** | `POST /orders` returns `202` immediately |

## Swapping in a real broker

Implement `EventBus` with a Kafka client (topic = `Topic`, `orderId` = partition
key so one order's events stay ordered), register it in `src/bootstrap.ts`, and
**no business logic changes**. That seam is the entire payoff of the design.

## What a production version adds next

- **Refund saga** when the restaurant rejects a paid order (compensating transaction)
- **Outbox pattern** so DB write + event publish are atomic (no lost/ghost events)
- **Schema registry** (Avro/Protobuf) for versioned event contracts
- **Partitioning by `orderId`** for ordering + horizontal scale
- **Idempotent producer / transactional writes** on the broker
```
