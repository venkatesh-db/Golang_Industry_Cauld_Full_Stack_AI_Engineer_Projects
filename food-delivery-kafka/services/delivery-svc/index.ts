import { EventType, Topics } from '../../packages/contracts/topics.js';
import { makeEvent, type EventEnvelope } from '../../packages/contracts/envelope.js';
import { emitEvents, type ConsumerSpec } from '../../packages/runtime/service.js';

const chance = (p: number) => Math.random() < p;
const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));

const RIDERS = ['Arjun', 'Priya', 'Ravi', 'Sana', 'Vikram', 'Meera'];

/** Assign a rider on acceptance; if none free, throw so the bus retries. */
const dispatch: ConsumerSpec = {
  group: 'delivery',
  topics: [Topics.RestaurantEvents],
  handler: async (e: EventEnvelope) => {
    if (e.type !== EventType.RestaurantAccepted) return;
    await sleep(200 + Math.random() * 400);
    if (chance(0.2)) {
      await emitEvents([makeEvent(EventType.RiderUnavailable, e.orderId, {})]);
      throw new Error('no rider available, will retry');
    }
    const name = RIDERS[Math.floor(Math.random() * RIDERS.length)];
    await emitEvents([
      makeEvent(EventType.RiderAssigned, e.orderId, {
        riderId: `rider_${Math.floor(Math.random() * 900 + 100)}`,
        riderName: name,
      }),
    ]);
  },
};

/** Last mile: once food is ready, pick up and deliver. */
const lastMile: ConsumerSpec = {
  group: 'delivery-lastmile',
  topics: [Topics.RestaurantEvents],
  handler: async (e: EventEnvelope) => {
    if (e.type !== EventType.FoodReady) return;
    await sleep(300 + Math.random() * 500);
    await emitEvents([makeEvent(EventType.OrderPickedUp, e.orderId, {})]);
    await sleep(1000 + Math.random() * 2000);
    await emitEvents([makeEvent(EventType.OrderDelivered, e.orderId, {})]);
  },
};

export const deliveryConsumers: ConsumerSpec[] = [dispatch, lastMile];
