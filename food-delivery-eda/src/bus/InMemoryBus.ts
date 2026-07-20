import { randomUUID } from 'node:crypto';
import type { DomainEvent, Topic } from '../events/types.js';
import type { EventBus, EventHandler, PublishInput } from './EventBus.js';

interface Consumer {
  group: string;
  handler: EventHandler;
  queue: DomainEvent[];
  draining: boolean;
  /** Ids already processed by this group — gives exactly-once *effect*. */
  processed: Set<string>;
}

const MAX_ATTEMPTS = 4;
const RETRY_BASE_MS = 200;

/**
 * A single-process stand-in for a durable log broker (Kafka/RabbitMQ). It
 * reproduces the semantics that actually matter for correctness:
 *   - fan-out to independent consumer GROUPS
 *   - at-least-once delivery with bounded RETRY (exponential backoff)
 *   - a DEAD-LETTER path when a message can never be processed
 *   - per-consumer sequential processing so one order's events stay ordered
 * Consumers must be IDEMPOTENT; the bus guards against re-delivery per group.
 */
export class InMemoryBus implements EventBus {
  private consumers = new Map<Topic, Consumer[]>();
  private anyListeners: ((e: DomainEvent) => void)[] = [];
  private log: DomainEvent[] = []; // the append-only event log (event sourcing)

  subscribe(topic: Topic, group: string, handler: EventHandler): void {
    const list = this.consumers.get(topic) ?? [];
    list.push({ group, handler, queue: [], draining: false, processed: new Set() });
    this.consumers.set(topic, list);
  }

  onAny(listener: (e: DomainEvent) => void): void {
    this.anyListeners.push(listener);
  }

  async publish(input: PublishInput): Promise<DomainEvent> {
    const event: DomainEvent = {
      id: randomUUID(),
      type: input.type,
      orderId: input.orderId,
      timestamp: new Date().toISOString(),
      attempt: 1,
      payload: input.payload ?? {},
    };
    this.log.push(event);
    for (const l of this.anyListeners) l(event);

    for (const consumer of this.consumers.get(event.type) ?? []) {
      consumer.queue.push(event);
      void this.drain(consumer);
    }
    return event;
  }

  /** Process a consumer's queue one event at a time (preserves ordering). */
  private async drain(consumer: Consumer): Promise<void> {
    if (consumer.draining) return;
    consumer.draining = true;
    try {
      while (consumer.queue.length > 0) {
        const event = consumer.queue.shift()!;
        if (consumer.processed.has(event.id)) continue; // idempotent skip
        await this.deliver(consumer, event);
      }
    } finally {
      consumer.draining = false;
    }
  }

  private async deliver(consumer: Consumer, event: DomainEvent): Promise<void> {
    try {
      await consumer.handler(event);
      consumer.processed.add(event.id);
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err);
      if (event.attempt >= MAX_ATTEMPTS) {
        console.error(
          `☠️  DLQ  [${consumer.group}] ${event.type} order=${event.orderId} ` +
            `after ${event.attempt} attempts: ${msg}`,
        );
        return; // dead-letter: give up, never block the partition forever
      }
      const retry: DomainEvent = { ...event, attempt: event.attempt + 1 };
      const backoff = RETRY_BASE_MS * 2 ** (event.attempt - 1);
      console.warn(
        `🔁 retry [${consumer.group}] ${event.type} order=${event.orderId} ` +
          `attempt ${retry.attempt}/${MAX_ATTEMPTS} in ${backoff}ms: ${msg}`,
      );
      setTimeout(() => {
        consumer.queue.push(retry);
        void this.drain(consumer);
      }, backoff);
    }
  }

  getLog(): readonly DomainEvent[] {
    return this.log;
  }
}
