import type { DomainEvent, Topic } from '../events/types.js';

export type EventHandler = (event: DomainEvent) => Promise<void>;

export interface PublishInput {
  type: Topic;
  orderId: string;
  payload?: Record<string, unknown>;
}

/**
 * The seam that isolates business logic from the broker. Swap InMemoryBus for
 * a KafkaBus / RabbitBus / SqsBus implementing this same interface and no
 * service code changes.
 */
export interface EventBus {
  /**
   * Register a consumer. `group` models a Kafka consumer group: events on the
   * topic are delivered to every distinct group exactly once (per group).
   */
  subscribe(topic: Topic, group: string, handler: EventHandler): void;
  publish(input: PublishInput): Promise<DomainEvent>;
  /** Observe every event flowing through the bus (used by the API/read model). */
  onAny(listener: (event: DomainEvent) => void): void;
}
