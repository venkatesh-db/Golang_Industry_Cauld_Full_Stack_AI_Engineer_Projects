/**
 * Single source of truth for Kafka topics and the event-type → topic map.
 * Topic-per-bounded-context (ADR-001 D2). Every event is keyed by orderId (D3).
 */
export const Topics = {
  OrderEvents: 'order-events',
  PaymentEvents: 'payment-events',
  RestaurantEvents: 'restaurant-events',
  DeliveryEvents: 'delivery-events',
  NotificationEvents: 'notification-events',
  DeadLetter: 'dead-letter',
} as const;

export type Topic = (typeof Topics)[keyof typeof Topics];

export const ALL_TOPICS: Topic[] = [
  Topics.OrderEvents,
  Topics.PaymentEvents,
  Topics.RestaurantEvents,
  Topics.DeliveryEvents,
  Topics.NotificationEvents,
  Topics.DeadLetter,
];

/** Every event type produced in the system. */
export const EventType = {
  OrderPlaced: 'order.placed',
  OrderCancelled: 'order.cancelled',
  PaymentRequested: 'payment.requested',
  PaymentAuthorized: 'payment.authorized',
  PaymentFailed: 'payment.failed',
  RefundRequested: 'refund.requested',
  RefundCompleted: 'refund.completed',
  RestaurantNotified: 'restaurant.notified',
  RestaurantAccepted: 'restaurant.accepted',
  RestaurantRejected: 'restaurant.rejected',
  FoodPreparing: 'food.preparing',
  FoodReady: 'food.ready',
  RiderAssigned: 'rider.assigned',
  RiderUnavailable: 'rider.unavailable',
  OrderPickedUp: 'order.picked_up',
  OrderDelivered: 'order.delivered',
} as const;

export type EventTypeName = (typeof EventType)[keyof typeof EventType];

/** Which topic each event type is published to. */
export const EVENT_TOPIC: Record<EventTypeName, Topic> = {
  [EventType.OrderPlaced]: Topics.OrderEvents,
  [EventType.OrderCancelled]: Topics.OrderEvents,
  [EventType.OrderPickedUp]: Topics.OrderEvents,
  [EventType.OrderDelivered]: Topics.OrderEvents,
  [EventType.PaymentRequested]: Topics.PaymentEvents,
  [EventType.PaymentAuthorized]: Topics.PaymentEvents,
  [EventType.PaymentFailed]: Topics.PaymentEvents,
  [EventType.RefundRequested]: Topics.PaymentEvents,
  [EventType.RefundCompleted]: Topics.PaymentEvents,
  [EventType.RestaurantNotified]: Topics.RestaurantEvents,
  [EventType.RestaurantAccepted]: Topics.RestaurantEvents,
  [EventType.RestaurantRejected]: Topics.RestaurantEvents,
  [EventType.FoodPreparing]: Topics.RestaurantEvents,
  [EventType.FoodReady]: Topics.RestaurantEvents,
  [EventType.RiderAssigned]: Topics.DeliveryEvents,
  [EventType.RiderUnavailable]: Topics.DeliveryEvents,
};

export function topicForEvent(type: EventTypeName): Topic {
  const topic = EVENT_TOPIC[type];
  if (!topic) throw new Error(`No topic mapped for event type ${type}`);
  return topic;
}
