import { Kafka, logLevel } from 'kafkajs';
import { config } from '../config.js';

/** Shared KafkaJS client factory (ADR-001 D1: KRaft broker, PLAINTEXT). */
export function createKafka(): Kafka {
  return new Kafka({
    clientId: config.kafkaClientId,
    brokers: config.kafkaBrokers,
    logLevel: logLevel.NOTHING,
    retry: { initialRetryTime: 300, retries: 8 },
  });
}
