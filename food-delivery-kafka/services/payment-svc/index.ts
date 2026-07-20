import { EventType, Topics } from '../../packages/contracts/topics.js';
import { makeEvent, type EventEnvelope } from '../../packages/contracts/envelope.js';
import type { PaymentRequestedPayload, RefundPayload } from '../../packages/domain/events.js';
import { emitEvents, type ConsumerSpec } from '../../packages/runtime/service.js';
import { getOrder } from '../../packages/db/ordersRepo.js';

const chance = (p: number) => Math.random() < p;
const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));

/**
 * Payment consumer (group "payment"). Charges on request, and completes refunds.
 * The gateway is flaky: transient errors are thrown (bus retries → possibly DLT);
 * a decline is a business outcome (payment.failed), NOT retried.
 */
const payment: ConsumerSpec = {
  group: 'payment',
  topics: [Topics.PaymentEvents],
  handler: async (e: EventEnvelope) => {
    if (e.type === EventType.PaymentRequested) {
      const p = e.payload as unknown as PaymentRequestedPayload;
      await sleep(150 + Math.random() * 300); // gateway round-trip
      if (chance(0.12)) throw new Error('payment gateway timeout'); // transient → retry
      if (chance(0.08)) {
        await emitEvents([makeEvent(EventType.PaymentFailed, e.orderId, { reason: 'card_declined' })]);
        return;
      }
      await emitEvents([
        makeEvent(EventType.PaymentAuthorized, e.orderId, { amount: p.amount, txnId: crypto.randomUUID() }),
      ]);
      return;
    }

    if (e.type === EventType.RefundRequested) {
      const p = e.payload as unknown as RefundPayload;
      await sleep(200); // refund gateway
      await emitEvents([
        makeEvent(EventType.RefundCompleted, e.orderId, { amount: p.amount, reason: p.reason, txnId: crypto.randomUUID() }),
      ]);
    }
  },
};

/**
 * Saga participant (group "payment-saga"). When a PAID order is rejected by the
 * restaurant, start the compensating refund (choreography — ADR-001 D8).
 */
const saga: ConsumerSpec = {
  group: 'payment-saga',
  topics: [Topics.RestaurantEvents],
  handler: async (e: EventEnvelope) => {
    if (e.type !== EventType.RestaurantRejected) return;
    const order = await getOrder(e.orderId);
    const amount = order?.amount ? Number(order.amount) : 0;
    await emitEvents([
      makeEvent(EventType.RefundRequested, e.orderId, { amount, reason: 'restaurant_rejected' }),
    ]);
  },
};

export const paymentConsumers: ConsumerSpec[] = [payment, saga];
