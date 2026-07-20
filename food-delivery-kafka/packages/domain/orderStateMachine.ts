import { EventType, type EventTypeName } from '../contracts/topics.js';

export type OrderStatus =
  | 'PLACED'
  | 'PAID'
  | 'PAYMENT_FAILED'
  | 'ACCEPTED'
  | 'REJECTED'
  | 'REJECTED_REFUNDED'
  | 'PREPARING'
  | 'READY'
  | 'RIDER_ASSIGNED'
  | 'PICKED_UP'
  | 'DELIVERED'
  | 'CANCELLED';

/** Event type → the order status it drives the read model into. */
const TRANSITIONS: Partial<Record<EventTypeName, OrderStatus>> = {
  [EventType.OrderPlaced]: 'PLACED',
  [EventType.PaymentAuthorized]: 'PAID',
  [EventType.PaymentFailed]: 'PAYMENT_FAILED',
  [EventType.RestaurantAccepted]: 'ACCEPTED',
  [EventType.RestaurantRejected]: 'REJECTED',
  [EventType.RefundCompleted]: 'REJECTED_REFUNDED',
  [EventType.FoodPreparing]: 'PREPARING',
  [EventType.FoodReady]: 'READY',
  [EventType.RiderAssigned]: 'RIDER_ASSIGNED',
  [EventType.OrderPickedUp]: 'PICKED_UP',
  [EventType.OrderDelivered]: 'DELIVERED',
  [EventType.OrderCancelled]: 'CANCELLED',
};

const TERMINAL: ReadonlySet<OrderStatus> = new Set([
  'DELIVERED',
  'CANCELLED',
  'REJECTED_REFUNDED',
  'PAYMENT_FAILED',
]);

/** Human-ordered lifecycle for the UI stepper (excludes failure branches). */
export const HAPPY_PATH: OrderStatus[] = [
  'PLACED',
  'PAID',
  'ACCEPTED',
  'PREPARING',
  'READY',
  'RIDER_ASSIGNED',
  'PICKED_UP',
  'DELIVERED',
];

export function statusForEvent(type: EventTypeName): OrderStatus | undefined {
  return TRANSITIONS[type];
}

export function isTerminal(status: OrderStatus): boolean {
  return TERMINAL.has(status);
}
