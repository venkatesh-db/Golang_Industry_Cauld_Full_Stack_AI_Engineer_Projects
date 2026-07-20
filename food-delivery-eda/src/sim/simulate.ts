import { bootstrap } from '../bootstrap.js';
import { sleep } from '../util.js';
import type { OrderPlacedPayload } from '../events/types.js';

/**
 * Fires a burst of orders through the bus (no HTTP) and prints the final
 * outcome of each — showing the happy path plus the failure branches
 * (declined payment, restaurant rejection, DLQ) that EDA lets us handle.
 */
const MENU: OrderPlacedPayload[] = [
  { customerId: 'c1', restaurantId: 'r1', items: [{ name: 'Biryani', qty: 1, price: 280 }], amount: 280, address: 'MG Road' },
  { customerId: 'c2', restaurantId: 'r2', items: [{ name: 'Masala Dosa', qty: 2, price: 120 }], amount: 240, address: 'HSR Layout' },
  { customerId: 'c3', restaurantId: 'r1', items: [{ name: 'Butter Chicken', qty: 1, price: 340 }], amount: 340, address: 'Indiranagar' },
  { customerId: 'c4', restaurantId: 'r3', items: [{ name: 'Veg Thali', qty: 1, price: 199 }], amount: 199, address: 'Koramangala' },
  { customerId: 'c5', restaurantId: 'r2', items: [{ name: 'Pizza', qty: 1, price: 450 }], amount: 450, address: 'Whitefield' },
];

async function main(): Promise<void> {
  const app = bootstrap();
  app.bus.onAny((e) =>
    console.log(`📨 ${e.type.padEnd(22)} order=${e.orderId.slice(0, 8)}`),
  );

  console.log(`\n🚀 Placing a burst of ${MENU.length} orders…\n`);
  const ids = await Promise.all(MENU.map((o) => app.orders.placeOrder(o)));

  await sleep(9000); // let the async lifecycles play out

  console.log('\n────────── FINAL STATE ──────────');
  for (const id of ids) {
    const v = app.store.get(id);
    if (!v) continue;
    const path = v.timeline.map((t) => t.status).join(' → ');
    console.log(`\norder ${id.slice(0, 8)}  [${v.status}]${v.riderId ? `  ${v.riderId}` : ''}`);
    console.log(`  ${path}`);
  }
  console.log('\n✅ Simulation complete.\n');
  process.exit(0);
}

void main();
