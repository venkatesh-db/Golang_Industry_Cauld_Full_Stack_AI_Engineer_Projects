import type { EventBus } from '../bus/EventBus.js';
import { Topics, type Topic } from '../events/types.js';

/**
 * Customer-facing notifications. A pure consumer — it produces no events, it
 * just reacts. Adding a new channel (WhatsApp, email) later means adding
 * another consumer here; no other service changes. That is the whole point of
 * event-driven design: new behavior = new subscriber.
 */
const MESSAGES: Partial<Record<Topic, string>> = {
  [Topics.OrderPlaced]: '🧾 Order placed! Waiting for payment…',
  [Topics.PaymentAuthorized]: '💳 Payment successful.',
  [Topics.PaymentFailed]: '❌ Payment failed — please try another method.',
  [Topics.RestaurantAccepted]: '👨‍🍳 Restaurant accepted your order.',
  [Topics.RestaurantRejected]: '🙁 Restaurant could not accept your order. Refund on the way.',
  [Topics.FoodReady]: '🍱 Your food is ready.',
  [Topics.RiderAssigned]: '🛵 A rider has been assigned.',
  [Topics.OrderPickedUp]: '📦 Order picked up — on its way!',
  [Topics.OrderDelivered]: '✅ Delivered. Enjoy your meal!',
};

export class NotificationService {
  constructor(private bus: EventBus) {}

  register(): void {
    for (const [topic, message] of Object.entries(MESSAGES)) {
      this.bus.subscribe(topic as Topic, 'notifications', async (e) => {
        console.log(`  🔔 [notify] order=${e.orderId.slice(0, 8)} — ${message}`);
      });
    }
  }
}
