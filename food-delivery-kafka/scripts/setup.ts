import { createKafka } from '../packages/kafka/client.js';
import { ensureTopics } from '../packages/kafka/admin.js';
import { migrate } from '../packages/db/migrate.js';
import { pool } from '../packages/db/pool.js';

/** One-shot: apply DB migrations and create all Kafka topics. Run after infra:up. */
async function main(): Promise<void> {
  console.log('Running migrations…');
  await migrate();
  console.log('Ensuring Kafka topics…');
  await ensureTopics(createKafka());
  await pool.end();
  console.log('\n✓ setup complete — start services with `npm run all`');
  process.exit(0);
}

void main().catch((err) => {
  console.error('setup failed:', err);
  process.exit(1);
});
