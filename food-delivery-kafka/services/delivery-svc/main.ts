import { startService } from '../../packages/runtime/service.js';
import { deliveryConsumers } from './index.js';

void startService('delivery-svc', deliveryConsumers).catch((err) => {
  console.error('delivery-svc fatal:', err);
  process.exit(1);
});
