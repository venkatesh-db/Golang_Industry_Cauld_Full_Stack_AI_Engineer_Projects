'use client';
import Link from 'next/link';
import { useEffect, useState } from 'react';
import { useCart } from '../lib/cart-store';

export function TopBar() {
  const count = useCart((s) => s.count());
  const [mounted, setMounted] = useState(false);
  useEffect(() => setMounted(true), []);

  return (
    <header className="topbar">
      <div className="container topbar-inner">
        <Link href="/" className="logo">feastly</Link>
        <div className="search">🔍 Search for restaurants, cuisines and dishes</div>
        <Link href="/cart" className="cart-btn" aria-label="View cart">
          🛒 Cart
          {mounted && count > 0 && <span className="cart-badge">{count}</span>}
        </Link>
      </div>
    </header>
  );
}
