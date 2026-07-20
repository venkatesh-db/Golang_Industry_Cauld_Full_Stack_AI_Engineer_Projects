import type { PoolClient } from '../db/pool.js';
import { topicForEvent } from '../contracts/topics.js';
import type { EventEnvelope } from '../contracts/envelope.js';

/**
 * Append an event to the outbox INSIDE an existing business transaction
 * (ADR-001 D6). Because the outbox row and the state change commit atomically,
 * a crash can never leave state changed but the event unpublished (or vice
 * versa) — the relay drains the row afterwards.
 */
export async function withOutbox(client: PoolClient, event: EventEnvelope): Promise<void> {
  await client.query(
    `INSERT INTO outbox (id, aggregate_id, topic, msg_key, payload)
     VALUES ($1,$2,$3,$4,$5) ON CONFLICT (id) DO NOTHING`,
    [event.id, event.orderId, topicForEvent(event.type), event.orderId, JSON.stringify(event)],
  );
}
