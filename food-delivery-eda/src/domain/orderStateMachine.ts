import { Topics, type Topic } from '../events/types.js';

/** The lifecycle an order moves through, one event at a time. */
export type OrderStatus =
  | 'PLACED'
  | 'PAID'
  | 'PAYMENT_FAILED'
  | 'ACCEPTED'
  | 'REJECTED'
  | 'PREPARING'
  | 'READY'
  | 'RIDER_ASSIGNED'
  | 'PICKED_UP'
  | 'DELIVERED'
  | 'CANCELLED';

/**
 * Which event drives the order read-model into which status. This is the one
 * place that knows the shape of the happy path (and the terminal branches).
 */
const TRANSITIONS: Partial<Record<Topic, OrderStatus>> = {
  [Topics.OrderPlaced]: 'PLACED',
  [Topics.PaymentAuthorized]: 'PAID',
  [Topics.PaymentFailed]: 'PAYMENT_FAILED',
  [Topics.RestaurantAccepted]: 'ACCEPTED',
  [Topics.RestaurantRejected]: 'REJECTED',
  [Topics.FoodPreparing]: 'PREPARING',
  [Topics.FoodReady]: 'READY',
  [Topics.RiderAssigned]: 'RIDER_ASSIGNED',
  [Topics.OrderPickedUp]: 'PICKED_UP',
  [Topics.OrderDelivered]: 'DELIVERED',
  [Topics.OrderCancelled]: 'CANCELLED',
};

const TERMINAL: ReadonlySet<OrderStatus> = new Set([
  'DELIVERED',
  'CANCELLED',
  'REJECTED',
  'PAYMENT_FAILED',
]);

export function nextStatus(event: Topic): OrderStatus | undefined {
  return TRANSITIONS[event];
}

export function isTerminal(status: OrderStatus): boolean {
  return TERMINAL.has(status);
}
