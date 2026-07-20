import { EventEmitter } from 'node:events';
import type { DomainEvent } from '../events/types.js';
import type { OrderStatus } from '../domain/orderStateMachine.js';

export interface OrderTimelineEntry {
  status: OrderStatus;
  event: string;
  at: string;
}

export interface OrderView {
  orderId: string;
  status: OrderStatus;
  customerId?: string;
  restaurantId?: string;
  riderId?: string;
  amount?: number;
  timeline: OrderTimelineEntry[];
  updatedAt: string;
}

/**
 * The materialized read model — a projection built purely by folding the event
 * stream. Nothing writes to it except by reacting to events, which is what
 * makes the whole system replayable: drop this and rebuild from the log.
 */
export class OrderStore extends EventEmitter {
  private orders = new Map<string, OrderView>();

  upsert(orderId: string, patch: Partial<OrderView>, event: DomainEvent, status: OrderStatus): OrderView {
    const existing = this.orders.get(orderId);
    const view: OrderView = {
      orderId,
      status,
      timeline: existing?.timeline ?? [],
      ...existing,
      ...patch,
      updatedAt: event.timestamp,
    };
    view.status = status;
    view.timeline = [
      ...(existing?.timeline ?? []),
      { status, event: event.type, at: event.timestamp },
    ];
    this.orders.set(orderId, view);
    this.emit('update', view);
    return view;
  }

  get(orderId: string): OrderView | undefined {
    return this.orders.get(orderId);
  }

  all(): OrderView[] {
    return [...this.orders.values()];
  }
}
