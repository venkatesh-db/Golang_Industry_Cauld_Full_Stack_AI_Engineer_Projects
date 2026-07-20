'use client';
import Link from 'next/link';
import { useCart } from '../../lib/cart-store';
import { VegDot } from '../../components/ui';

const DELIVERY_FEE = 40;
const PLATFORM_FEE = 6;

export default function CartPage() {
  const { lines, restaurantName, add, remove, subtotal, restaurantId } = useCart();

  if (lines.length === 0) {
    return (
      <div className="container empty">
        <p style={{ fontSize: 48 }}>🛒</p>
        <h2>Your cart is empty</h2>
        <p>Add items from a restaurant to get started.</p>
        <Link href="/" className="link">Browse restaurants →</Link>
      </div>
    );
  }

  const sub = subtotal();
  const taxes = Math.round(sub * 0.05);
  const total = sub + DELIVERY_FEE + PLATFORM_FEE + taxes;

  return (
    <div className="container" style={{ maxWidth: 640 }}>
      <h2 className="section-title" style={{ marginTop: 24 }}>{restaurantName}</h2>
      <div className="panel">
        {lines.map((l) => (
          <div key={l.id} className="mitem" style={{ padding: '14px 0' }}>
            <div><VegDot veg={l.veg} /> <span style={{ fontWeight: 600 }}> {l.name}</span></div>
            <div style={{ display: 'flex', alignItems: 'center', gap: 20 }}>
              <div className="stepper">
                <button onClick={() => remove(l.id)} aria-label={`Remove one ${l.name}`}>−</button>
                <span>{l.qty}</span>
                <button onClick={() => add(restaurantId!, restaurantName!, l)} aria-label={`Add one ${l.name}`}>+</button>
              </div>
              <span style={{ fontWeight: 600, minWidth: 60, textAlign: 'right' }}>₹{l.qty * l.price}</span>
            </div>
          </div>
        ))}
      </div>

      <div className="panel">
        <div className="bill-row"><span>Item total</span><span>₹{sub}</span></div>
        <div className="bill-row"><span>Delivery fee</span><span>₹{DELIVERY_FEE}</span></div>
        <div className="bill-row"><span>Platform fee</span><span>₹{PLATFORM_FEE}</span></div>
        <div className="bill-row"><span>Taxes (GST)</span><span>₹{taxes}</span></div>
        <div className="bill-row total"><span>To Pay</span><span>₹{total}</span></div>
        <Link href="/checkout"><button className="cta">Proceed to Pay · ₹{total}</button></Link>
      </div>
    </div>
  );
}
