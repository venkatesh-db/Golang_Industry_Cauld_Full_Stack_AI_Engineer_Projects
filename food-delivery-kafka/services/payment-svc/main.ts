import { startService } from '../../packages/runtime/service.js';
import { paymentConsumers } from './index.js';

void startService('payment-svc', paymentConsumers).catch((err) => {
  console.error('payment-svc fatal:', err);
  process.exit(1);
});
