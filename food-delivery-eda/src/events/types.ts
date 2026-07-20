/**
 * The event catalog. In a real deployment each `Topic` maps 1:1 to a Kafka
 * topic (or a RabbitMQ routing key / SNS topic). Producers never call another
 * service directly — they only ever publish one of these facts.
 */

export const Topics = {
  OrderPlaced: 'order.placed',
  PaymentRequested: 'payment.requested',
  PaymentAuthorized: 'payment.authorized',
  PaymentFailed: 'payment.failed',
  RestaurantNotified: 'restaurant.notified',
  RestaurantAccepted: 'restaurant.accepted',
  RestaurantRejected: 'restaurant.rejected',
  FoodPreparing: 'food.preparing',
  FoodReady: 'food.ready',
  RiderAssigned: 'rider.assigned',
  RiderUnavailable: 'rider.unavailable',
  OrderPickedUp: 'order.picked_up',
  OrderDelivered: 'order.delivered',
  OrderCancelled: 'order.cancelled',
} as const;

export type Topic = (typeof Topics)[keyof typeof Topics];

/** The immutable envelope every fact travels in. */
export interface DomainEvent<T = Record<string, unknown>> {
  /** Unique id — the basis for idempotent (exactly-once-effect) consumption. */
  id: string;
  type: Topic;
  /** Partition key. Kafka would use this to keep one order's events ordered. */
  orderId: string;
  timestamp: string;
  attempt: number;
  payload: T;
}

export interface OrderItem {
  name: string;
  qty: number;
  price: number;
}

export interface OrderPlacedPayload {
  customerId: string;
  restaurantId: string;
  items: OrderItem[];
  amount: number;
  address: string;
}
