import { startService } from '../../packages/runtime/service.js';
import { notificationConsumers } from './index.js';

void startService('notification-svc', notificationConsumers).catch((err) => {
  console.error('notification-svc fatal:', err);
  process.exit(1);
});
