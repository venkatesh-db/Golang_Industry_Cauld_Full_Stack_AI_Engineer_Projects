import type { Kafka } from 'kafkajs';
import { ALL_TOPICS } from '../contracts/topics.js';
import { config } from '../config.js';

/** Idempotently create all topics with the configured partition count (ADR-001 D2). */
export async function ensureTopics(kafka: Kafka): Promise<void> {
  const admin = kafka.admin();
  await admin.connect();
  try {
    const existing = new Set(await admin.listTopics());
    const toCreate = ALL_TOPICS.filter((t) => !existing.has(t)).map((topic) => ({
      topic,
      numPartitions: config.topicPartitions,
      replicationFactor: 1,
    }));
    if (toCreate.length > 0) {
      await admin.createTopics({ topics: toCreate, waitForLeaders: true });
      console.log(`✓ created topics: ${toCreate.map((t) => t.topic).join(', ')}`);
    } else {
      console.log('✓ topics already present');
    }
  } finally {
    await admin.disconnect();
  }
}
