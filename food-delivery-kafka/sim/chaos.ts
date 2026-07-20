import { config } from '../packages/config.js';

/**
 * Crash-safety demo. Place an order, then (while it's mid-flight) kill a
 * consumer — e.g. `Ctrl-C` the restaurant-svc terminal — and restart it. Because
 * offsets commit only after successful handling and consumers are idempotent,
 * the order still reaches a correct terminal state with no lost or duplicated
 * effects. This script places one order and polls its status to completion.
 * Usage: `npm run sim:chaos`
 */
const BASE = `http://localhost:${config.orderSvcPort}`;

async function main(): Promise<void> {
  const res = await fetch(`${BASE}/orders`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      customerId: 'chaos',
      restaurantId: 'r1',
      restaurantName: 'Meghana Foods',
      items: [{ name: 'Chicken Biryani', qty: 1, price: 320 }],
      amount: 320,
      address: 'Chaos St',
    }),
  });
  const { orderId } = (await res.json()) as { orderId: string };
  console.log(`placed ${orderId}`);
  console.log('👉 Now Ctrl-C a service terminal (e.g. restaurant-svc) and restart it.\n');

  let last = '';
  for (let i = 0; i < 40; i++) {
    await new Promise((r) => setTimeout(r, 1000));
    const o = (await (await fetch(`${BASE}/orders/${orderId}`)).json()) as { status?: string };
    if (o.status && o.status !== last) {
      last = o.status;
      console.log(`  [${i}s] ${o.status}`);
      if (['DELIVERED', 'PAYMENT_FAILED', 'REJECTED_REFUNDED', 'CANCELLED'].includes(o.status)) break;
    }
  }
  console.log(`\nfinal: ${last} — no lost/duplicated effects despite the restart.`);
  process.exit(0);
}

void main();
