import { config } from '../packages/config.js';

/**
 * Fire a burst of concurrent orders at order-svc to exercise partitioning and
 * consumer-group parallelism. Watch consumer lag build and recover in Kafka UI
 * (http://localhost:8080). Usage: `npm run sim:burst -- 50`
 */
const N = Number(process.argv[2] ?? 20);
const BASE = `http://localhost:${config.orderSvcPort}`;

const DISHES = [
  { restaurantId: 'r1', restaurantName: 'Meghana Foods', name: 'Chicken Biryani', price: 320 },
  { restaurantId: 'r2', restaurantName: 'Truffles', name: 'Veg Burger', price: 240 },
  { restaurantId: 'r3', restaurantName: 'CTR', name: 'Masala Dosa', price: 120 },
  { restaurantId: 'r4', restaurantName: 'Empire', name: 'Butter Chicken', price: 340 },
];

async function place(i: number): Promise<string | null> {
  const d = DISHES[i % DISHES.length];
  try {
    const res = await fetch(`${BASE}/orders`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        customerId: `cust_${i}`,
        restaurantId: d.restaurantId,
        restaurantName: d.restaurantName,
        items: [{ name: d.name, qty: 1, price: d.price }],
        amount: d.price,
        address: 'Test Address',
      }),
    });
    const body = (await res.json()) as { orderId?: string };
    return body.orderId ?? null;
  } catch {
    return null;
  }
}

async function main(): Promise<void> {
  console.log(`🚀 firing ${N} concurrent orders at ${BASE}…`);
  const ids = (await Promise.all(Array.from({ length: N }, (_, i) => place(i)))).filter(Boolean) as string[];
  console.log(`✓ placed ${ids.length}/${N} orders`);

  await new Promise((r) => setTimeout(r, 9000)); // let lifecycles play out

  const counts: Record<string, number> = {};
  for (const id of ids) {
    try {
      const res = await fetch(`${BASE}/orders/${id}`);
      const o = (await res.json()) as { status?: string };
      counts[o.status ?? 'UNKNOWN'] = (counts[o.status ?? 'UNKNOWN'] ?? 0) + 1;
    } catch {
      counts.ERROR = (counts.ERROR ?? 0) + 1;
    }
  }
  console.log('\n── final status distribution ──');
  for (const [status, n] of Object.entries(counts).sort((a, b) => b[1] - a[1])) {
    console.log(`  ${status.padEnd(20)} ${n}`);
  }
  process.exit(0);
}

void main();
