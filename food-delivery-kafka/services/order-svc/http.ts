import { createServer, type IncomingMessage, type ServerResponse } from 'node:http';
import { EventType } from '../../packages/contracts/topics.js';
import { makeEvent } from '../../packages/contracts/envelope.js';
import type { OrderPlacedPayload } from '../../packages/domain/events.js';
import { emitEvents } from '../../packages/runtime/service.js';
import { getOrder } from '../../packages/db/ordersRepo.js';
import { config } from '../../packages/config.js';

const send = (res: ServerResponse, code: number, body: unknown) => {
  res.writeHead(code, { 'Content-Type': 'application/json', 'Access-Control-Allow-Origin': '*' });
  res.end(JSON.stringify(body));
};

const readBody = (req: IncomingMessage): Promise<string> =>
  new Promise((resolve) => {
    let raw = '';
    req.on('data', (c) => (raw += c));
    req.on('end', () => resolve(raw));
  });

/**
 * The one synchronous command in the system. It emits `order.placed` via the
 * outbox (atomic) and returns 202 immediately — everything downstream is async.
 */
export function startHttp(): void {
  const server = createServer(async (req, res) => {
    if (req.method === 'OPTIONS') return send(res, 204, {});
    const url = new URL(req.url ?? '/', `http://localhost:${config.orderSvcPort}`);

    if (req.method === 'POST' && url.pathname === '/orders') {
      try {
        const b = JSON.parse((await readBody(req)) || '{}') as Partial<OrderPlacedPayload>;
        const event = makeEvent(EventType.OrderPlaced, crypto.randomUUID(), {
          customerId: b.customerId ?? 'guest',
          restaurantId: b.restaurantId ?? 'r1',
          restaurantName: b.restaurantName ?? 'Unknown',
          items: b.items ?? [],
          amount: b.amount ?? 0,
          address: b.address ?? '',
        } satisfies OrderPlacedPayload);
        await emitEvents([event]);
        return send(res, 202, { orderId: event.orderId, status: 'PLACED', track: `/orders/${event.orderId}` });
      } catch (err) {
        console.error('POST /orders failed:', err);
        return send(res, 400, { error: 'invalid order' });
      }
    }

    const match = url.pathname.match(/^\/orders\/([^/]+)$/);
    if (req.method === 'GET' && match) {
      const order = await getOrder(match[1]);
      return order ? send(res, 200, order) : send(res, 404, { error: 'not found' });
    }

    send(res, 404, { error: 'route not found' });
  });

  server.listen(config.orderSvcPort, () =>
    console.log(`  ✓ order-svc HTTP on http://localhost:${config.orderSvcPort}`),
  );
}
