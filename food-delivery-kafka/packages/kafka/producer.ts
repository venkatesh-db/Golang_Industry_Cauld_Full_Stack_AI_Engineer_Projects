import type { Kafka, Producer } from 'kafkajs';
import type { EventEnvelope } from '../contracts/envelope.js';
import { topicForEvent } from '../contracts/topics.js';

/**
 * Thin producer that routes an event to its topic (from the contract map) and
 * keys it by orderId so all events for one order land on the same partition.
 */
export class EventProducer {
  private producer: Producer;
  private connected = false;

  constructor(kafka: Kafka) {
    this.producer = kafka.producer({ allowAutoTopicCreation: false, idempotent: true });
  }

  async connect(): Promise<void> {
    if (!this.connected) {
      await this.producer.connect();
      this.connected = true;
    }
  }

  async publish(event: EventEnvelope): Promise<void> {
    await this.connect();
    await this.producer.send({
      topic: topicForEvent(event.type),
      messages: [{ key: event.orderId, value: JSON.stringify(event) }],
    });
  }

  async disconnect(): Promise<void> {
    if (this.connected) await this.producer.disconnect();
    this.connected = false;
  }
}
