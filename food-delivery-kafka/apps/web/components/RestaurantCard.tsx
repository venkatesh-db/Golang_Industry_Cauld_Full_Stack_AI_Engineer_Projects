import Link from 'next/link';
import type { Restaurant } from '../lib/types';
import { RatingPill } from './ui';

export function RestaurantCard({ r }: { r: Restaurant }) {
  return (
    <Link href={`/restaurant/${r.id}`} className="rcard">
      <div className="thumb">
        {r.emoji}
        {r.offer && <span className="offer">{r.offer}</span>}
      </div>
      <h3>{r.name}</h3>
      <div className="meta">
        <RatingPill rating={r.rating} />
        <span>•</span>
        <span>{r.etaMins} mins</span>
        <span>•</span>
        <span>₹{r.priceForTwo} for two</span>
      </div>
      <div className="cuisines">{r.cuisines.join(', ')}</div>
    </Link>
  );
}
