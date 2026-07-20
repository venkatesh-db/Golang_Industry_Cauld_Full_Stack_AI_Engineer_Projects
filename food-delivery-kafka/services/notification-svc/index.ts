import { EventType, Topics, type EventTypeName } from '../../packages/contracts/topics.js';
import type { EventEnvelope } from '../../packages/contracts/envelope.js';
import type { ConsumerSpec } from '../../packages/runtime/service.js';

/** Customer-facing notifications. Pure consumer — reacts, never produces. */
const MESSAGES: Partial<Record<EventTypeName, string>> = {
  [EventType.OrderPlaced]: '🧾 Order placed! Confirming payment…',
  [EventType.PaymentAuthorized]: '💳 Payment successful.',
  [EventType.PaymentFailed]: '❌ Payment failed — try another method.',
  [EventType.RestaurantAccepted]: '👨‍🍳 Restaurant accepted your order.',
  [EventType.RestaurantRejected]: '🙁 Restaurant could not accept — refund on the way.',
  [EventType.RefundCompleted]: '💸 Refund completed.',
  [EventType.FoodReady]: '🍱 Your food is ready.',
  [EventType.RiderAssigned]: '🛵 A rider is assigned.',
  [EventType.OrderPickedUp]: '📦 Picked up — on the way!',
  [EventType.OrderDelivered]: '✅ Delivered. Enjoy!',
};

const notifications: ConsumerSpec = {
  group: 'notifications',
  topics: [Topics.OrderEvents, Topics.PaymentEvents, Topics.RestaurantEvents, Topics.DeliveryEvents],
  handler: async (e: EventEnvelope) => {
    const msg = MESSAGES[e.type];
    if (msg) console.log(`  🔔 order=${e.orderId.slice(0, 8)} — ${msg}`);
  },
};

export const notificationConsumers: ConsumerSpec[] = [notifications];
