import type { EventBus } from '../bus/EventBus.js';
import { Topics } from '../events/types.js';
import { chance, jitter } from '../util.js';

/**
 * Charges the customer. Talks to a flaky "payment gateway": ~15% of attempts
 * throw a transient error, which the bus retries (at-least-once). If the card
 * is genuinely declined (~8%), it emits payment.failed — a business outcome,
 * not an error, so it is NOT retried.
 */
export class PaymentService {
  constructor(private bus: EventBus, private failRate = 0.15, private declineRate = 0.08) {}

  register(): void {
    this.bus.subscribe(Topics.PaymentRequested, 'payments', async (e) => {
      await jitter(150, 500); // gateway round-trip

      if (chance(this.failRate)) {
        // Transient gateway blip — throw so the bus retries with backoff.
        throw new Error('payment gateway timeout');
      }

      if (chance(this.declineRate)) {
        await this.bus.publish({
          type: Topics.PaymentFailed,
          orderId: e.orderId,
          payload: { reason: 'card_declined' },
        });
        return;
      }

      await this.bus.publish({
        type: Topics.PaymentAuthorized,
        orderId: e.orderId,
        payload: { amount: e.payload.amount, txnId: crypto.randomUUID() },
      });
    });
  }
}
