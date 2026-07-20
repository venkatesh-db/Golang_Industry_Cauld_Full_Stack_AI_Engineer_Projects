'use client';
import Link from 'next/link';
import { useEffect, useState } from 'react';
import { useCart } from '../lib/cart-store';

/** Floating cart bar shown at the bottom when the cart has items. */
export function CartBar() {
  const { count, subtotal } = useCart();
  const [mounted, setMounted] = useState(false);
  useEffect(() => setMounted(true), []);
  const n = count();
  if (!mounted || n === 0) return null;

  return (
    <div className="cartbar">
      <span>{n} item{n > 1 ? 's' : ''} · ₹{subtotal()}</span>
      <Link href="/cart">View Cart →</Link>
    </div>
  );
}
