import { EventType, Topics } from '../../packages/contracts/topics.js';
import { makeEvent, type EventEnvelope } from '../../packages/contracts/envelope.js';
import { statusForEvent } from '../../packages/domain/orderStateMachine.js';
import type {
  OrderPlacedPayload,
  RiderAssignedPayload,
} from '../../packages/domain/events.js';
import { projectOrder, type OrderRow } from '../../packages/db/ordersRepo.js';
import { emitEvents, type ConsumerSpec } from '../../packages/runtime/service.js';

/**
 * Projection consumer (group "projection"): folds EVERY business event across
 * all topics into the read model. This is the single writer of order status.
 */
const projection: ConsumerSpec = {
  group: 'projection',
  topics: [Topics.OrderEvents, Topics.PaymentEvents, Topics.RestaurantEvents, Topics.DeliveryEvents],
  handler: async (e: EventEnvelope) => {
    const status = statusForEvent(e.type);
    if (!status) return; // internal events (payment.requested, restaurant.notified, refund.requested)
    const patch: Partial<OrderRow> = {};
    if (e.type === EventType.OrderPlaced) {
      const p = e.payload as unknown as OrderPlacedPayload;
      patch.customer_id = p.customerId;
      patch.restaurant_id = p.restaurantId;
      patch.restaurant_name = p.restaurantName;
      patch.amount = String(p.amount);
    }
    if (e.type === EventType.RiderAssigned) {
      const p = e.payload as unknown as RiderAssignedPayload;
      patch.rider_id = p.riderId;
      patch.rider_name = p.riderName;
    }
    if (e.type === EventType.RefundCompleted) patch.refund_status = 'REFUNDED';
    await projectOrder(e.orderId, status, e.type, patch);
  },
};

/**
 * Orchestration consumer (group "order-orchestrator"): a placed order triggers
 * a payment request. Emitted crash-safely via the outbox.
 */
const orchestration: ConsumerSpec = {
  group: 'order-orchestrator',
  topics: [Topics.OrderEvents],
  handler: async (e: EventEnvelope) => {
    if (e.type !== EventType.OrderPlaced) return;
    const p = e.payload as unknown as OrderPlacedPayload;
    await emitEvents([
      makeEvent(EventType.PaymentRequested, e.orderId, {
        amount: p.amount,
        customerId: p.customerId,
      }),
    ]);
  },
};

export const orderConsumers: ConsumerSpec[] = [projection, orchestration];
