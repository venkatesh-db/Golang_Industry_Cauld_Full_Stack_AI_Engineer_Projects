import { startService } from '../../packages/runtime/service.js';
import { orderConsumers } from './consumers.js';
import { startHttp } from './http.js';

async function main(): Promise<void> {
  await startService('order-svc', orderConsumers);
  startHttp();
}

void main().catch((err) => {
  console.error('order-svc fatal:', err);
  process.exit(1);
});
