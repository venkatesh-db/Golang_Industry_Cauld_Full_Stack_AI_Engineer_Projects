export function VegDot({ veg }: { veg: boolean }) {
  return <span className={`veg-dot ${veg ? 'veg' : 'nonveg'}`} aria-label={veg ? 'Vegetarian' : 'Non-vegetarian'} role="img" />;
}

export function RatingPill({ rating }: { rating: number }) {
  return <span className="rating-pill">★ {rating.toFixed(1)}</span>;
}
