import { randomUUID } from 'node:crypto';
import type { EventTypeName } from './topics.js';

/**
 * The immutable envelope every event travels in. `orderId` is the Kafka message
 * key → guarantees per-order ordering across partitions (ADR-001 D3).
 */
export interface EventEnvelope<T = Record<string, unknown>> {
  id: string;
  type: EventTypeName;
  orderId: string;
  version: number;
  occurredAt: string;
  attempt: number;
  payload: T;
}

export function makeEvent<T extends Record<string, unknown>>(
  type: EventTypeName,
  orderId: string,
  payload: T,
): EventEnvelope<T> {
  return {
    id: randomUUID(),
    type,
    orderId,
    version: 1,
    occurredAt: new Date().toISOString(),
    attempt: 1,
    payload,
  };
}

/**
 * Lightweight runtime validation of the envelope shape. (JSON contracts, ADR
 * living-doc deviation: Schema Registry container is available for inspection;
 * Avro/SR encoding is the documented fast-follow.)
 */
export function validateEnvelope(e: unknown): asserts e is EventEnvelope {
  if (typeof e !== 'object' || e === null) throw new Error('event is not an object');
  const ev = e as Record<string, unknown>;
  for (const key of ['id', 'type', 'orderId', 'occurredAt'] as const) {
    if (typeof ev[key] !== 'string' || !ev[key]) throw new Error(`event.${key} missing/invalid`);
  }
  if (typeof ev.payload !== 'object' || ev.payload === null) throw new Error('event.payload invalid');
}
