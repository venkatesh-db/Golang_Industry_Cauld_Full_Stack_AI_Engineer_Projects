'use client';
import type { MenuItem } from '../lib/types';
import { useCart } from '../lib/cart-store';
import { VegDot } from './ui';

export function MenuItemCard({ item, restaurantId, restaurantName }: {
  item: MenuItem;
  restaurantId: string;
  restaurantName: string;
}) {
  const { add, remove, lines } = useCart();
  const qty = lines.find((l) => l.id === item.id)?.qty ?? 0;

  return (
    <div className="mitem">
      <div>
        <VegDot veg={item.veg} />
        {item.bestseller && <span className="bestseller"> ★ Bestseller</span>}
        <h4>{item.name}</h4>
        <div className="price">₹{item.price}</div>
        <div className="desc">{item.desc}</div>
      </div>
      <div className="mitem-right">
        {qty === 0 ? (
          <button className="add-btn" onClick={() => add(restaurantId, restaurantName, item)} aria-label={`Add ${item.name}`}>
            Add
          </button>
        ) : (
          <div className="stepper">
            <button onClick={() => remove(item.id)} aria-label={`Remove one ${item.name}`}>−</button>
            <span>{qty}</span>
            <button onClick={() => add(restaurantId, restaurantName, item)} aria-label={`Add one more ${item.name}`}>+</button>
          </div>
        )}
      </div>
    </div>
  );
}
