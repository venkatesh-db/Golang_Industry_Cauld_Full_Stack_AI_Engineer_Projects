import { InMemoryBus } from './bus/InMemoryBus.js';
import { OrderStore } from './store/store.js';
import { OrderService } from './services/orderService.js';
import { PaymentService } from './services/paymentService.js';
import { RestaurantService } from './services/restaurantService.js';
import { DeliveryService } from './services/deliveryService.js';
import { NotificationService } from './services/notificationService.js';

/** Wire the bus, read model, and all consumers together. */
export function bootstrap() {
  const bus = new InMemoryBus();
  const store = new OrderStore();

  const orders = new OrderService(bus, store);
  orders.register();
  new PaymentService(bus).register();
  new RestaurantService(bus).register();
  new DeliveryService(bus).register();
  new NotificationService(bus).register();

  return { bus, store, orders };
}

export type App = ReturnType<typeof bootstrap>;
