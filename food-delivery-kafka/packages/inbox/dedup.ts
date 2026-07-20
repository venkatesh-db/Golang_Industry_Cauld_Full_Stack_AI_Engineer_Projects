import Redis from 'ioredis';
import { pool } from '../db/pool.js';
import { config } from '../config.js';
import type { Dedup } from '../kafka/consumer.js';

/**
 * Idempotency (ADR-001 D5). A Redis fast-path (short TTL) shortcuts the common
 * "already seen" case; Postgres `processed_events` is the durable source of
 * truth. Together with at-least-once delivery this yields effectively-once
 * *effects* — the central distributed-systems guarantee of this build.
 */
export function createDedup(): Dedup & { close: () => Promise<void> } {
  const redis = new Redis(config.redisUrl, { lazyConnect: false, maxRetriesPerRequest: 2 });
  const key = (group: string, id: string) => `inbox:${group}:${id}`;

  return {
    async alreadyProcessed(group, eventId) {
      try {
        if (await redis.exists(key(group, eventId))) return true;
      } catch {
        /* redis down → fall through to durable check */
      }
      const { rowCount } = await pool.query(
        'SELECT 1 FROM processed_events WHERE consumer_group = $1 AND event_id = $2',
        [group, eventId],
      );
      return (rowCount ?? 0) > 0;
    },

    async markProcessed(group, eventId) {
      await pool.query(
        `INSERT INTO processed_events (consumer_group, event_id) VALUES ($1,$2)
         ON CONFLICT DO NOTHING`,
        [group, eventId],
      );
      try {
        await redis.set(key(group, eventId), '1', 'EX', 3600);
      } catch {
        /* best-effort cache */
      }
    },

    async close() {
      redis.disconnect();
    },
  };
}
