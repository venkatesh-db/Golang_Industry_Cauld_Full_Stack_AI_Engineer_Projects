import type { OrderStatus, OrderView } from '../lib/types';

const STEPS: { status: OrderStatus; title: string; sub: string }[] = [
  { status: 'PLACED', title: 'Order placed', sub: 'We received your order' },
  { status: 'PAID', title: 'Payment confirmed', sub: 'Your payment went through' },
  { status: 'ACCEPTED', title: 'Restaurant accepted', sub: 'The restaurant is on it' },
  { status: 'PREPARING', title: 'Preparing your food', sub: 'Cooking in progress' },
  { status: 'READY', title: 'Food is ready', sub: 'Packed and ready' },
  { status: 'RIDER_ASSIGNED', title: 'Rider assigned', sub: 'On the way to pick up' },
  { status: 'PICKED_UP', title: 'Picked up', sub: 'Your order is on the move' },
  { status: 'DELIVERED', title: 'Delivered', sub: 'Enjoy your meal!' },
];

const ORDER = STEPS.map((s) => s.status);

export function OrderStepper({ order }: { order: OrderView }) {
  const failed = order.status === 'PAYMENT_FAILED';
  const rejected = order.status === 'REJECTED' || order.status === 'REJECTED_REFUNDED';
  const reachedIdx = ORDER.indexOf(order.status);

  return (
    <div>
      {STEPS.map((step, i) => {
        const done = reachedIdx >= 0 && i <= reachedIdx;
        const active = i === reachedIdx && order.status !== 'DELIVERED';
        const last = i === STEPS.length - 1;
        return (
          <div key={step.status} className={`step ${done ? 'done' : ''} ${active ? 'active' : ''}`}>
            <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'center' }}>
              <div className="dot">{done ? '✓' : i + 1}</div>
              {!last && <div className="line" />}
            </div>
            <div className="step-label">
              <div className="t">{step.title}</div>
              <div className="s">{step.sub}</div>
            </div>
          </div>
        );
      })}
      {failed && <p className="s" style={{ color: 'var(--primary)' }}>Payment failed — no charge was made.</p>}
      {rejected && (
        <p className="s" style={{ color: 'var(--primary)' }}>
          Restaurant couldn’t accept the order. {order.status === 'REJECTED_REFUNDED' ? 'Refund completed 💸' : 'Processing refund…'}
        </p>
      )}
    </div>
  );
}
