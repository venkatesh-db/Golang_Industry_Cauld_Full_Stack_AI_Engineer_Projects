import type { Kafka, Producer } from 'kafkajs';
import type { Topic } from '../contracts/topics.js';
import { validateEnvelope, type EventEnvelope } from '../contracts/envelope.js';
import { toDeadLetter } from './dlt.js';

export type EventHandler = (event: EventEnvelope) => Promise<void>;

/** Optional idempotency hooks (implemented by packages/inbox). */
export interface Dedup {
  alreadyProcessed: (group: string, eventId: string) => Promise<boolean>;
  markProcessed: (group: string, eventId: string) => Promise<void>;
}

export interface ConsumerOptions {
  kafka: Kafka;
  groupId: string;
  topics: Topic[];
  handler: EventHandler;
  producer: Producer; // for dead-lettering
  dedup?: Dedup;
  maxAttempts?: number;
}

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));

/**
 * The delivery core (ADR-001 D5/D7). At-least-once: the offset is committed only
 * after the handler succeeds (eachMessage auto-commits on normal return; a throw
 * would re-deliver). We add:
 *   - idempotency: skip if this group already processed the event id
 *   - bounded in-handler retry with backoff
 *   - dead-letter routing on exhaustion, then commit (never block the partition)
 */
export async function runConsumer(opts: ConsumerOptions): Promise<() => Promise<void>> {
  const { kafka, groupId, topics, handler, producer, dedup, maxAttempts = 4 } = opts;
  const consumer = kafka.consumer({ groupId, sessionTimeout: 30000 });
  await consumer.connect();
  for (const topic of topics) await consumer.subscribe({ topic, fromBeginning: true });

  await consumer.run({
    autoCommit: true,
    eachMessage: async ({ topic, message }) => {
      if (!message.value) return;
      let event: EventEnvelope;
      try {
        const parsed = JSON.parse(message.value.toString());
        validateEnvelope(parsed);
        event = parsed;
      } catch (err) {
        // Unparseable → straight to DLT, then commit.
        await toDeadLetter(producer, topic, groupId, {} as EventEnvelope, `parse: ${String(err)}`);
        return;
      }

      if (dedup && (await dedup.alreadyProcessed(groupId, event.id))) return; // idempotent skip

      for (let attempt = 1; attempt <= maxAttempts; attempt++) {
        try {
          await handler({ ...event, attempt });
          if (dedup) await dedup.markProcessed(groupId, event.id);
          return;
        } catch (err) {
          const msg = err instanceof Error ? err.message : String(err);
          if (attempt >= maxAttempts) {
            console.error(`☠️  DLT [${groupId}] ${event.type} order=${event.orderId.slice(0, 8)}: ${msg}`);
            await toDeadLetter(producer, topic, groupId, { ...event, attempt }, msg);
            return; // commit, move on
          }
          const backoff = 200 * 2 ** (attempt - 1);
          console.warn(`🔁 retry [${groupId}] ${event.type} attempt ${attempt + 1}/${maxAttempts} in ${backoff}ms`);
          await sleep(backoff);
        }
      }
    },
  });

  return async () => {
    await consumer.disconnect();
  };
}
