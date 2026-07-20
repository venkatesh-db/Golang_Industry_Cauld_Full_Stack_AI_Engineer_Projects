'use client';
import { create } from 'zustand';
import { persist } from 'zustand/middleware';
import type { MenuItem } from './types';

export interface CartLine extends MenuItem {
  qty: number;
}

interface CartState {
  restaurantId: string | null;
  restaurantName: string | null;
  lines: CartLine[];
  add: (restaurantId: string, restaurantName: string, item: MenuItem) => void;
  remove: (itemId: string) => void;
  clear: () => void;
  count: () => number;
  subtotal: () => number;
}

/**
 * Cart state (persisted to localStorage). Swiggy/Zomato rule: a cart belongs to
 * one restaurant — adding from another replaces it.
 */
export const useCart = create<CartState>()(
  persist(
    (set, get) => ({
      restaurantId: null,
      restaurantName: null,
      lines: [],
      add: (restaurantId, restaurantName, item) =>
        set((s) => {
          const switching = s.restaurantId && s.restaurantId !== restaurantId;
          const lines = switching ? [] : [...s.lines];
          const existing = lines.find((l) => l.id === item.id);
          if (existing) existing.qty += 1;
          else lines.push({ ...item, qty: 1 });
          return { restaurantId, restaurantName, lines };
        }),
      remove: (itemId) =>
        set((s) => {
          const lines = s.lines
            .map((l) => (l.id === itemId ? { ...l, qty: l.qty - 1 } : l))
            .filter((l) => l.qty > 0);
          return lines.length === 0
            ? { lines, restaurantId: null, restaurantName: null }
            : { lines };
        }),
      clear: () => set({ lines: [], restaurantId: null, restaurantName: null }),
      count: () => get().lines.reduce((n, l) => n + l.qty, 0),
      subtotal: () => get().lines.reduce((n, l) => n + l.qty * l.price, 0),
    }),
    { name: 'fdk-cart' },
  ),
);
