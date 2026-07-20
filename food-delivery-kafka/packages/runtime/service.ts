import type { Kafka, Producer } from 'kafkajs';
import { createKafka } from '../kafka/client.js';
import { ensureTopics } from '../kafka/admin.js';
import { runConsumer, type Dedup, type EventHandler } from '../kafka/consumer.js';
import { startOutboxRelay } from '../outbox/relay.js';
import { withOutbox } from '../outbox/withOutbox.js';
import { createDedup } from '../inbox/dedup.js';
import { migrate } from '../db/migrate.js';
import { pool } from '../db/pool.js';
import type { Topic } from '../contracts/topics.js';
import type { EventEnvelope } from '../contracts/envelope.js';

export interface ConsumerSpec {
  group: string;
  topics: Topic[];
  handler: EventHandler;
}

export interface Runtime {
  kafka: Kafka;
  dedup: Dedup;
  stop: () => Promise<void>;
}

/**
 * One shared bootstrap for every service (reuse gate): migrate schema, ensure
 * topics, connect a DLT producer, start the outbox relay, wire the dedup, and
 * run the given consumers. Keeps each service's main.ts to just its handlers.
 */
export async function startService(name: string, consumers: ConsumerSpec[]): Promise<Runtime> {
  console.log(`\n▶ starting ${name}…`);
  const kafka = createKafka();
  await migrate();
  await ensureTopics(kafka);

  const dltProducer: Producer = kafka.producer({ allowAutoTopicCreation: false });
  await dltProducer.connect();
  const dedup = createDedup();
  const stopRelay = startOutboxRelay(kafka);

  const stoppers: Array<() => Promise<void>> = [];
  for (const c of consumers) {
    const stop = await runConsumer({
      kafka,
      groupId: c.group,
      topics: c.topics,
      handler: c.handler,
      producer: dltProducer,
      dedup,
    });
    stoppers.push(stop);
    console.log(`  ✓ consumer [${c.group}] on ${c.topics.join(', ')}`);
  }
  console.log(`✓ ${name} ready`);

  return {
    kafka,
    dedup,
    stop: async () => {
      await Promise.all(stoppers.map((s) => s()));
      await stopRelay();
      await dltProducer.disconnect();
      await dedup.close();
    },
  };
}

/**
 * Emit one or more events crash-safely via the transactional outbox. All events
 * commit atomically; the relay publishes them to Kafka afterwards.
 */
export async function emitEvents(events: EventEnvelope[]): Promise<void> {
  const client = await pool.connect();
  try {
    await client.query('BEGIN');
    for (const e of events) await withOutbox(client, e);
    await client.query('COMMIT');
  } catch (err) {
    await client.query('ROLLBACK');
    throw err;
  } finally {
    client.release();
  }
}
