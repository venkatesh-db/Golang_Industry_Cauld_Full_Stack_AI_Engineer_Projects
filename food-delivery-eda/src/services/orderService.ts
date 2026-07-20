import type { EventBus } from '../bus/EventBus.js';
import type { OrderStore } from '../store/store.js';
import { Topics, type DomainEvent, type OrderPlacedPayload } from '../events/types.js';
import { nextStatus } from '../domain/orderStateMachine.js';

/**
 * Owns the order aggregate. Two jobs:
 *   1. Accept new orders (the only synchronous entry point) and emit the fact.
 *   2. Project EVERY lifecycle event into the read model — this is the
 *      single writer of order status, folding the stream via the state machine.
 * It also kicks off payment, because "an order was placed" is the trigger for
 * "please charge the customer".
 */
export class OrderService {
  constructor(private bus: EventBus, private store: OrderStore) {}

  register(): void {
    // Orchestration: a placed order needs a payment.
    this.bus.subscribe(Topics.OrderPlaced, 'orders', async (e) => {
      const p = e.payload as unknown as OrderPlacedPayload;
      await this.bus.publish({
        type: Topics.PaymentRequested,
        orderId: e.orderId,
        payload: { amount: p.amount, customerId: p.customerId },
      });
    });

    // Projection: fold every known event into the read model.
    for (const topic of Object.values(Topics)) {
      this.bus.subscribe(topic, 'read-model', async (e) => this.project(e));
    }
  }

  private async project(e: DomainEvent): Promise<void> {
    const status = nextStatus(e.type);
    if (!status) return; // e.g. payment.requested / restaurant.notified are internal
    const patch: Record<string, unknown> = {};
    if (e.type === Topics.OrderPlaced) {
      const p = e.payload as unknown as OrderPlacedPayload;
      patch.customerId = p.customerId;
      patch.restaurantId = p.restaurantId;
      patch.amount = p.amount;
    }
    if (e.type === Topics.RiderAssigned) patch.riderId = e.payload.riderId;
    this.store.upsert(e.orderId, patch, e, status);
  }

  async placeOrder(input: OrderPlacedPayload): Promise<string> {
    const event = await this.bus.publish({
      type: Topics.OrderPlaced,
      orderId: crypto.randomUUID(),
      payload: input as unknown as Record<string, unknown>,
    });
    return event.orderId;
  }
}
