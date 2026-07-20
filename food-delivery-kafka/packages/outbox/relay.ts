import type { Kafka, Producer } from 'kafkajs';
import { pool } from '../db/pool.js';

interface OutboxRow {
  id: string;
  topic: string;
  msg_key: string;
  payload: unknown;
}

/**
 * The outbox relay (ADR-001 D6). Polls unsent rows using
 * `FOR UPDATE SKIP LOCKED` so multiple relay instances never grab the same row,
 * publishes to Kafka, then marks them sent — all in one transaction so a crash
 * mid-publish simply re-runs the row (at-least-once; consumers dedup).
 */
export function startOutboxRelay(kafka: Kafka, opts: { intervalMs?: number; batch?: number } = {}) {
  const intervalMs = opts.intervalMs ?? 500;
  const batch = opts.batch ?? 100;
  const producer: Producer = kafka.producer({ allowAutoTopicCreation: false, idempotent: true });
  let stopped = false;
  let connected = false;

  async function tick(): Promise<void> {
    if (!connected) {
      await producer.connect();
      connected = true;
    }
    const client = await pool.connect();
    try {
      await client.query('BEGIN');
      const { rows } = await client.query<OutboxRow>(
        `SELECT id, topic, msg_key, payload FROM outbox
         WHERE sent_at IS NULL ORDER BY created_at
         FOR UPDATE SKIP LOCKED LIMIT $1`,
        [batch],
      );
      if (rows.length > 0) {
        const byTopic = new Map<string, { key: string; value: string }[]>();
        for (const r of rows) {
          const list = byTopic.get(r.topic) ?? [];
          list.push({ key: r.msg_key, value: JSON.stringify(r.payload) });
          byTopic.set(r.topic, list);
        }
        for (const [topic, messages] of byTopic) {
          await producer.send({ topic, messages });
        }
        await client.query(
          `UPDATE outbox SET sent_at = now() WHERE id = ANY($1::uuid[])`,
          [rows.map((r) => r.id)],
        );
      }
      await client.query('COMMIT');
    } catch (err) {
      await client.query('ROLLBACK');
      console.error('outbox relay error:', err instanceof Error ? err.message : err);
    } finally {
      client.release();
    }
  }

  async function loop(): Promise<void> {
    while (!stopped) {
      await tick();
      await new Promise((r) => setTimeout(r, intervalMs));
    }
  }

  void loop();
  console.log('✓ outbox relay started');
  return async () => {
    stopped = true;
    if (connected) await producer.disconnect();
  };
}
