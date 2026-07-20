import { createServer, type IncomingMessage, type ServerResponse } from 'node:http';
import type { App } from '../bootstrap.js';
import type { OrderPlacedPayload } from '../events/types.js';

const json = (res: ServerResponse, code: number, body: unknown): void => {
  const data = JSON.stringify(body, null, 2);
  res.writeHead(code, { 'Content-Type': 'application/json' });
  res.end(data);
};

const readBody = (req: IncomingMessage): Promise<string> =>
  new Promise((resolve) => {
    let raw = '';
    req.on('data', (c) => (raw += c));
    req.on('end', () => resolve(raw));
  });

export function startServer(app: App, port = 3000): void {
  const { store, orders } = app;

  const server = createServer(async (req, res) => {
    const url = new URL(req.url ?? '/', `http://localhost:${port}`);
    const path = url.pathname;

    // POST /orders — place an order (the one synchronous command)
    if (req.method === 'POST' && path === '/orders') {
      try {
        const body = JSON.parse((await readBody(req)) || '{}') as Partial<OrderPlacedPayload>;
        const input: OrderPlacedPayload = {
          customerId: body.customerId ?? 'cust_demo',
          restaurantId: body.restaurantId ?? 'rest_1',
          items: body.items ?? [{ name: 'Paneer Butter Masala', qty: 1, price: 320 }],
          amount: body.amount ?? 320,
          address: body.address ?? '221B Baker Street',
        };
        const orderId = await orders.placeOrder(input);
        return json(res, 202, { orderId, status: 'PLACED', track: `/orders/${orderId}` });
      } catch {
        return json(res, 400, { error: 'invalid JSON body' });
      }
    }

    // GET /orders — list all
    if (req.method === 'GET' && path === '/orders') {
      return json(res, 200, store.all());
    }

    // GET /orders/:id — single order with full timeline
    const match = path.match(/^\/orders\/([^/]+)$/);
    if (req.method === 'GET' && match) {
      const view = store.get(match[1]);
      return view ? json(res, 200, view) : json(res, 404, { error: 'not found' });
    }

    // GET /stream — live SSE feed of every order update
    if (req.method === 'GET' && path === '/stream') {
      res.writeHead(200, {
        'Content-Type': 'text/event-stream',
        'Cache-Control': 'no-cache',
        Connection: 'keep-alive',
      });
      res.write(`event: hello\ndata: "connected"\n\n`);
      const onUpdate = (view: unknown) => res.write(`data: ${JSON.stringify(view)}\n\n`);
      store.on('update', onUpdate);
      req.on('close', () => store.off('update', onUpdate));
      return;
    }

    json(res, 404, { error: 'route not found' });
  });

  server.listen(port, () => {
    console.log(`\n🍔 Food-delivery EDA up on http://localhost:${port}`);
    console.log('   POST /orders          place an order');
    console.log('   GET  /orders/:id      track one order (full timeline)');
    console.log('   GET  /orders          list all orders');
    console.log('   GET  /stream          live SSE feed\n');
  });
}
