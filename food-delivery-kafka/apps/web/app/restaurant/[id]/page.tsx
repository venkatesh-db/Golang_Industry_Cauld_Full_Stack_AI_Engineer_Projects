import { notFound } from 'next/navigation';
import { findRestaurant } from '../../../lib/catalog';
import { MenuItemCard } from '../../../components/MenuItemCard';
import { CartBar } from '../../../components/CartBar';
import { RatingPill } from '../../../components/ui';

export default async function RestaurantPage({ params }: { params: Promise<{ id: string }> }) {
  const { id } = await params;
  const r = findRestaurant(id);
  if (!r) notFound();

  return (
    <div className="container">
      <div className="rheader">
        <h1>{r.name}</h1>
        <div className="meta">
          <RatingPill rating={r.rating} /> &nbsp; {r.cuisines.join(', ')} · {r.etaMins} mins · ₹{r.priceForTwo} for two
        </div>
      </div>

      {r.menu.map((cat) => (
        <section key={cat.category}>
          <h2 className="menu-cat">{cat.category} ({cat.items.length})</h2>
          {cat.items.map((item) => (
            <MenuItemCard key={item.id} item={item} restaurantId={r.id} restaurantName={r.name} />
          ))}
        </section>
      ))}
      <div style={{ height: 80 }} />
      <CartBar />
    </div>
  );
}
