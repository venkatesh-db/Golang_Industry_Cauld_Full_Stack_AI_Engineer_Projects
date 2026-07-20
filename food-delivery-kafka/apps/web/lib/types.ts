export interface MenuItem {
  id: string;
  name: string;
  price: number;
  veg: boolean;
  desc: string;
  bestseller?: boolean;
}

export interface Restaurant {
  id: string;
  name: string;
  cuisines: string[];
  rating: number;
  etaMins: number;
  priceForTwo: number;
  offer?: string;
  emoji: string;
  menu: { category: string; items: MenuItem[] }[];
}

export type OrderStatus =
  | 'PLACED' | 'PAID' | 'PAYMENT_FAILED' | 'ACCEPTED' | 'REJECTED'
  | 'REJECTED_REFUNDED' | 'PREPARING' | 'READY' | 'RIDER_ASSIGNED'
  | 'PICKED_UP' | 'DELIVERED' | 'CANCELLED';

export interface OrderView {
  order_id: string;
  status: OrderStatus;
  restaurant_name: string | null;
  amount: string | null;
  rider_name: string | null;
  refund_status: string | null;
  timeline: { status: OrderStatus; event_type: string; at: string }[];
}
