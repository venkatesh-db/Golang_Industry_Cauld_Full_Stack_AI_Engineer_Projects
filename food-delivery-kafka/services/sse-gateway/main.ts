import { startService } from '../../packages/runtime/service.js';
import { gatewayConsumers } from './index.js';

void startService('sse-gateway', gatewayConsumers).catch((err) => {
  console.error('sse-gateway fatal:', err);
  process.exit(1);
});
