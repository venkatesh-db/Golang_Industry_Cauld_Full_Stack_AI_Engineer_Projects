'use client';
import { useRouter } from 'next/navigation';
import { useState } from 'react';
import { useCart } from '../../lib/cart-store';

export default function CheckoutPage() {
  const router = useRouter();
  const { lines, restaurantId, restaurantName, subtotal, clear } = useCart();
  const [placing, setPlacing] = useState(false);
  const [pay, setPay] = useState('upi');

  const total = subtotal() + 46 + Math.round(subtotal() * 0.05);

  async function placeOrder() {
    if (lines.length === 0) return;
    setPlacing(true);
    try {
      const res = await fetch('/api/orders', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          customerId: 'web-user',
          restaurantId,
          restaurantName,
          items: lines.map((l) => ({ name: l.name, qty: l.qty, price: l.price })),
          amount: total,
          address: '221B Baker Street, Bengaluru',
        }),
      });
      const data = (await res.json()) as { orderId?: string };
      if (!data.orderId) throw new Error('no orderId');
      clear();
      router.push(`/orders/${data.orderId}`);
    } catch {
      setPlacing(false);
      alert('Could not place order. Is order-svc running on :4000?');
    }
  }

  if (lines.length === 0) {
    return <div className="container empty"><h2>Nothing to checkout</h2></div>;
  }

  return (
    <div className="container" style={{ maxWidth: 640 }}>
      <h2 className="section-title" style={{ marginTop: 24 }}>Checkout</h2>
      <div className="panel">
        <strong>Deliver to</strong>
        <p style={{ color: 'var(--muted)', marginTop: 6 }}>🏠 Home · 221B Baker Street, Bengaluru 560001</p>
      </div>
      <div className="panel">
        <strong>Payment method</strong>
        <div className="pay-chip" style={{ marginTop: 14 }}>
          {[['upi', '📱 UPI'], ['card', '💳 Card'], ['cod', '💵 Cash']].map(([v, label]) => (
            <label key={v}>
              <input type="radio" name="pay" checked={pay === v} onChange={() => setPay(v)} />
              <span>{label}</span>
            </label>
          ))}
        </div>
        <button className="cta pay" onClick={placeOrder} disabled={placing}>
          {placing ? 'Placing order…' : `Place Order · ₹${total}`}
        </button>
      </div>
    </div>
  );
}
