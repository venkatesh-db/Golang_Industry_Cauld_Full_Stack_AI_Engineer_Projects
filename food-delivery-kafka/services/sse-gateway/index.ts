import Redis from 'ioredis';
import { Topics } from '../../packages/contracts/topics.js';
import type { EventEnvelope } from '../../packages/contracts/envelope.js';
import { statusForEvent } from '../../packages/domain/orderStateMachine.js';
import { config } from '../../packages/config.js';
import type { ConsumerSpec } from '../../packages/runtime/service.js';

const publisher = new Redis(config.redisUrl);

export const channelFor = (orderId: string) => `order:${orderId}`;

/**
 * Bridges Kafka → Redis pub/sub (ADR-001 D11). The Next.js /api/stream SSE
 * endpoint subscribes to `order:{id}` and streams deltas to the browser. Redis
 * fan-out lets multiple gateway/web instances scale horizontally.
 */
const bridge: ConsumerSpec = {
  group: 'sse-gateway',
  topics: [Topics.OrderEvents, Topics.PaymentEvents, Topics.RestaurantEvents, Topics.DeliveryEvents],
  handler: async (e: EventEnvelope) => {
    const status = statusForEvent(e.type);
    if (!status) return; // only broadcast status transitions
    const msg = JSON.stringify({
      orderId: e.orderId,
      type: e.type,
      status,
      at: e.occurredAt,
      payload: e.payload,
    });
    await publisher.publish(channelFor(e.orderId), msg);
  },
};

export const gatewayConsumers: ConsumerSpec[] = [bridge];
