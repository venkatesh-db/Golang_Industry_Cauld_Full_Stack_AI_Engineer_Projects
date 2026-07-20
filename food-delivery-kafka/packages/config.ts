import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

// Minimal .env loader (no dotenv dependency): read food-delivery-kafka/.env if present.
const root = join(dirname(fileURLToPath(import.meta.url)), '..');
try {
  const raw = readFileSync(join(root, '.env'), 'utf8');
  for (const line of raw.split('\n')) {
    const m = line.match(/^\s*([\w.]+)\s*=\s*(.*)\s*$/);
    if (m && !process.env[m[1]]) process.env[m[1]] = m[2].replace(/^["']|["']$/g, '');
  }
} catch {
  /* fall back to real env / defaults below */
}

export const config = {
  kafkaBrokers: (process.env.KAFKA_BROKERS ?? 'localhost:9094').split(','),
  kafkaClientId: process.env.KAFKA_CLIENT_ID ?? 'fdk',
  databaseUrl: process.env.DATABASE_URL ?? 'postgres://fdk:fdk@localhost:5432/fdk',
  redisUrl: process.env.REDIS_URL ?? 'redis://localhost:6379',
  orderSvcPort: Number(process.env.ORDER_SVC_PORT ?? 4000),
  sseGatewayPort: Number(process.env.SSE_GATEWAY_PORT ?? 4100),
  topicPartitions: Number(process.env.TOPIC_PARTITIONS ?? 6),
};
