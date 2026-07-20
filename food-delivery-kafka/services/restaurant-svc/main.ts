import { startService } from '../../packages/runtime/service.js';
import { restaurantConsumers } from './index.js';

void startService('restaurant-svc', restaurantConsumers).catch((err) => {
  console.error('restaurant-svc fatal:', err);
  process.exit(1);
});
