import type { EventBus } from '../bus/EventBus.js';
import { Topics } from '../events/types.js';
import { chance, jitter } from '../util.js';

/**
 * The restaurant side. Reacts to a successful payment, accepts (or rejects)
 * the order, then drives the kitchen: preparing -> ready. Each step is its own
 * event so downstream consumers (delivery, notifications, analytics) can react
 * independently without the restaurant knowing they exist.
 */
export class RestaurantService {
  constructor(private bus: EventBus, private rejectRate = 0.1) {}

  register(): void {
    this.bus.subscribe(Topics.PaymentAuthorized, 'restaurant', async (e) => {
      await this.bus.publish({ type: Topics.RestaurantNotified, orderId: e.orderId });
      await jitter(300, 900); // restaurant looks at the ticket

      if (chance(this.rejectRate)) {
        await this.bus.publish({
          type: Topics.RestaurantRejected,
          orderId: e.orderId,
          payload: { reason: 'out_of_stock' },
        });
        return; // NOTE: a real system would trigger a refund saga here.
      }

      await this.bus.publish({ type: Topics.RestaurantAccepted, orderId: e.orderId });
    });

    // Kitchen workflow, decoupled from acceptance.
    this.bus.subscribe(Topics.RestaurantAccepted, 'kitchen', async (e) => {
      await this.bus.publish({ type: Topics.FoodPreparing, orderId: e.orderId });
      await jitter(800, 2000); // cooking
      await this.bus.publish({ type: Topics.FoodReady, orderId: e.orderId });
    });
  }
}
