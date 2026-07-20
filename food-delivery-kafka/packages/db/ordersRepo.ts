import { pool } from './pool.js';
import type { OrderStatus } from '../domain/orderStateMachine.js';

export interface OrderRow {
  order_id: string;
  status: OrderStatus;
  customer_id: string | null;
  restaurant_id: string | null;
  restaurant_name: string | null;
  amount: string | null;
  rider_id: string | null;
  rider_name: string | null;
  refund_status: string | null;
  created_at: string;
  updated_at: string;
}

export interface TimelineRow {
  status: OrderStatus;
  event_type: string;
  at: string;
}

/** Upsert the read-model row and append a timeline entry (projection). */
export async function projectOrder(
  orderId: string,
  status: OrderStatus,
  eventType: string,
  patch: Partial<Omit<OrderRow, 'order_id' | 'status'>> = {},
): Promise<void> {
  const client = await pool.connect();
  try {
    await client.query('BEGIN');
    await client.query(
      `INSERT INTO orders (order_id, status, customer_id, restaurant_id, restaurant_name, amount, rider_id, rider_name, refund_status, updated_at)
       VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9, now())
       ON CONFLICT (order_id) DO UPDATE SET
         status = EXCLUDED.status,
         customer_id   = COALESCE(EXCLUDED.customer_id, orders.customer_id),
         restaurant_id = COALESCE(EXCLUDED.restaurant_id, orders.restaurant_id),
         restaurant_name = COALESCE(EXCLUDED.restaurant_name, orders.restaurant_name),
         amount        = COALESCE(EXCLUDED.amount, orders.amount),
         rider_id      = COALESCE(EXCLUDED.rider_id, orders.rider_id),
         rider_name    = COALESCE(EXCLUDED.rider_name, orders.rider_name),
         refund_status = COALESCE(EXCLUDED.refund_status, orders.refund_status),
         updated_at    = now()`,
      [
        orderId,
        status,
        patch.customer_id ?? null,
        patch.restaurant_id ?? null,
        patch.restaurant_name ?? null,
        patch.amount ?? null,
        patch.rider_id ?? null,
        patch.rider_name ?? null,
        patch.refund_status ?? null,
      ],
    );
    await client.query(
      `INSERT INTO order_timeline (order_id, status, event_type) VALUES ($1,$2,$3)`,
      [orderId, status, eventType],
    );
    await client.query('COMMIT');
  } catch (err) {
    await client.query('ROLLBACK');
    throw err;
  } finally {
    client.release();
  }
}

export async function getOrder(orderId: string): Promise<(OrderRow & { timeline: TimelineRow[] }) | null> {
  const { rows } = await pool.query<OrderRow>('SELECT * FROM orders WHERE order_id = $1', [orderId]);
  if (rows.length === 0) return null;
  const tl = await pool.query<TimelineRow>(
    'SELECT status, event_type, at FROM order_timeline WHERE order_id = $1 ORDER BY id',
    [orderId],
  );
  return { ...rows[0], timeline: tl.rows };
}
