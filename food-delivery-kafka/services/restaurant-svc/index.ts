import { EventType, Topics } from '../../packages/contracts/topics.js';
import { makeEvent, type EventEnvelope } from '../../packages/contracts/envelope.js';
import { emitEvents, type ConsumerSpec } from '../../packages/runtime/service.js';

const chance = (p: number) => Math.random() < p;
const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));

/** Reacts to a successful payment: accept or reject the order. */
const desk: ConsumerSpec = {
  group: 'restaurant',
  topics: [Topics.PaymentEvents],
  handler: async (e: EventEnvelope) => {
    if (e.type !== EventType.PaymentAuthorized) return;
    await emitEvents([makeEvent(EventType.RestaurantNotified, e.orderId, {})]);
    await sleep(300 + Math.random() * 600);
    if (chance(0.1)) {
      await emitEvents([makeEvent(EventType.RestaurantRejected, e.orderId, { reason: 'out_of_stock' })]);
      return;
    }
    await emitEvents([makeEvent(EventType.RestaurantAccepted, e.orderId, {})]);
  },
};

/** Kitchen workflow, decoupled from acceptance: preparing → ready. */
const kitchen: ConsumerSpec = {
  group: 'kitchen',
  topics: [Topics.RestaurantEvents],
  handler: async (e: EventEnvelope) => {
    if (e.type !== EventType.RestaurantAccepted) return;
    await emitEvents([makeEvent(EventType.FoodPreparing, e.orderId, {})]);
    await sleep(800 + Math.random() * 1500);
    await emitEvents([makeEvent(EventType.FoodReady, e.orderId, {})]);
  },
};

export const restaurantConsumers: ConsumerSpec[] = [desk, kitchen];
