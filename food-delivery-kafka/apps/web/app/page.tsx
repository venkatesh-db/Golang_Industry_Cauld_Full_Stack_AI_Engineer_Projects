import { CATALOG } from '../lib/catalog';
import { RestaurantCard } from '../components/RestaurantCard';
import { CartBar } from '../components/CartBar';

const CUISINES = ['🍕 Pizza', '🍔 Burger', '🍗 Biryani', '🥞 Dosa', '🍛 North Indian', '🍨 Desserts', '🍜 Chinese', '☕ Cafe'];

export default function Home() {
  return (
    <>
      <section className="hero">
        <div className="container">
          <h1>Order food to your door</h1>
          <p>Event-driven delivery, powered by Apache Kafka — watch every order flow in real time.</p>
        </div>
      </section>

      <div className="container">
        <div className="chips">
          {CUISINES.map((c) => <div key={c} className="chip">{c}</div>)}
        </div>

        <h2 className="section-title">{CATALOG.length} restaurants around you</h2>
        <div className="rgrid">
          {CATALOG.map((r) => <RestaurantCard key={r.id} r={r} />)}
        </div>
      </div>
      <CartBar />
    </>
  );
}
