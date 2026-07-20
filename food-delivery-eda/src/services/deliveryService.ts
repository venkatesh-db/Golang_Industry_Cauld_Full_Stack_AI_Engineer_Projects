import type { EventBus } from '../bus/EventBus.js';
import { Topics } from '../events/types.js';
import { chance, jitter } from '../util.js';

/**
 * Rider matching + last-mile. On acceptance it assigns a rider (if none are
 * free it throws, so the bus retries until one is — a crude but realistic
 * back-pressure model). Once the food is ready it drives pickup -> delivered.
 */
export class DeliveryService {
  constructor(private bus: EventBus, private noRiderRate = 0.2) {}

  register(): void {
    this.bus.subscribe(Topics.RestaurantAccepted, 'delivery', async (e) => {
      await jitter(200, 600);
      if (chance(this.noRiderRate)) {
        // No rider free right now — retry (backoff) instead of dropping.
        await this.bus.publish({ type: Topics.RiderUnavailable, orderId: e.orderId });
        throw new Error('no rider available, will retry');
      }
      await this.bus.publish({
        type: Topics.RiderAssigned,
        orderId: e.orderId,
        payload: { riderId: `rider_${Math.floor(Math.random() * 900 + 100)}` },
      });
    });

    this.bus.subscribe(Topics.FoodReady, 'delivery', async (e) => {
      await jitter(300, 800); // rider reaches the restaurant
      await this.bus.publish({ type: Topics.OrderPickedUp, orderId: e.orderId });
      await jitter(1000, 2500); // drives to the customer
      await this.bus.publish({ type: Topics.OrderDelivered, orderId: e.orderId });
    });
  }
}
