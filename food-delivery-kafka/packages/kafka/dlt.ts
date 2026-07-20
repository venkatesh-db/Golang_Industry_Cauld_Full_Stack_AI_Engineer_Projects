import type { Producer } from 'kafkajs';
import { Topics } from '../contracts/topics.js';
import type { EventEnvelope } from '../contracts/envelope.js';

/**
 * Route a poison message to the dead-letter topic with diagnostic headers
 * (ADR-001 D7) instead of blocking the partition forever.
 */
export async function toDeadLetter(
  producer: Producer,
  originalTopic: string,
  group: string,
  event: EventEnvelope,
  error: string,
): Promise<void> {
  await producer.send({
    topic: Topics.DeadLetter,
    messages: [
      {
        key: event.orderId,
        value: JSON.stringify(event),
        headers: {
          'x-original-topic': originalTopic,
          'x-consumer-group': group,
          'x-error': error,
          'x-attempts': String(event.attempt),
          'x-failed-at': new Date().toISOString(),
        },
      },
    ],
  });
}
