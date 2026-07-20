'use client';
import { useParams } from 'next/navigation';
import { useEffect, useState } from 'react';
import type { OrderStatus, OrderView } from '../../../lib/types';
import { OrderStepper } from '../../../components/OrderStepper';

const HERO: Partial<Record<OrderStatus, { title: string; sub: string; cls?: string }>> = {
  PLACED: { title: 'Order placed 🎉', sub: 'Confirming your payment…' },
  PAID: { title: 'Payment confirmed 💳', sub: 'Sending your order to the restaurant' },
  ACCEPTED: { title: 'Order accepted 👨‍🍳', sub: 'The kitchen is getting started' },
  PREPARING: { title: 'Preparing your food 🍳', sub: 'Freshly cooking your order' },
  READY: { title: 'Food is ready 🍱', sub: 'Waiting for the rider' },
  RIDER_ASSIGNED: { title: 'Rider assigned 🛵', sub: 'Heading to the restaurant' },
  PICKED_UP: { title: 'On the way 📦', sub: 'Your order is coming to you' },
  DELIVERED: { title: 'Delivered ✅', sub: 'Enjoy your meal!', cls: 'done' },
  PAYMENT_FAILED: { title: 'Payment failed ❌', sub: 'No charge was made', cls: 'fail' },
  REJECTED_REFUNDED: { title: 'Order refunded 💸', sub: 'Restaurant couldn’t accept it', cls: 'fail' },
};

export default function TrackPage() {
  const { id } = useParams<{ id: string }>();
  const [order, setOrder] = useState<OrderView | null>(null);

  useEffect(() => {
    let alive = true;
    // Backfill current state, then subscribe to live deltas (resync-on-connect).
    fetch(`/api/orders/${id}`).then((r) => r.json()).then((o) => alive && o?.order_id && setOrder(o));

    const es = new EventSource(`/api/stream?orderId=${id}`);
    es.onmessage = (ev) => {
      const d = JSON.parse(ev.data) as { status: OrderStatus; type: string; at: string; payload?: { riderName?: string } };
      setOrder((prev) => {
        const base: OrderView = prev ?? { order_id: id, status: d.status, restaurant_name: null, amount: null, rider_name: null, refund_status: null, timeline: [] };
        return {
          ...base,
          status: d.status,
          rider_name: d.payload?.riderName ?? base.rider_name,
          timeline: [...base.timeline, { status: d.status, event_type: d.type, at: d.at }],
        };
      });
    };
    return () => { alive = false; es.close(); };
  }, [id]);

  if (!order) return <div className="container track-wrap"><div className="skeleton" style={{ height: 120 }} /></div>;
  const hero = HERO[order.status] ?? { title: order.status, sub: '' };

  return (
    <div className="container track-wrap">
      <div className={`status-hero ${hero.cls ?? ''}`}>
        <h2>{hero.title}</h2>
        <p>{hero.sub}</p>
        {order.restaurant_name && <p style={{ marginTop: 8, fontWeight: 700 }}>{order.restaurant_name}{order.amount ? ` · ₹${order.amount}` : ''}</p>}
      </div>

      {order.rider_name && (
        <div className="panel rider-card">
          <div className="rider-avatar">🧑‍✈️</div>
          <div>
            <div style={{ fontWeight: 700 }}>{order.rider_name}</div>
            <div className="s" style={{ color: 'var(--muted)', fontSize: 13 }}>Your delivery partner</div>
          </div>
        </div>
      )}

      <div className="panel"><OrderStepper order={order} /></div>
      <p className="s" style={{ textAlign: 'center', color: 'var(--muted)', fontSize: 12 }}>
        Order #{order.order_id.slice(0, 8)} · live via Kafka → Redis → SSE
      </p>
    </div>
  );
}
