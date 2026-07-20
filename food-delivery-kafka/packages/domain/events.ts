/** Domain payload types carried inside the EventEnvelope for each event type. */

export interface OrderItem {
  name: string;
  qty: number;
  price: number;
}

export interface OrderPlacedPayload {
  customerId: string;
  restaurantId: string;
  restaurantName: string;
  items: OrderItem[];
  amount: number;
  address: string;
}

export interface PaymentRequestedPayload {
  amount: number;
  customerId: string;
}

export interface PaymentAuthorizedPayload {
  amount: number;
  txnId: string;
}

export interface PaymentFailedPayload {
  reason: string;
}

export interface RefundPayload {
  amount: number;
  reason: string;
  txnId?: string;
}

export interface RestaurantRejectedPayload {
  reason: string;
}

export interface RiderAssignedPayload {
  riderId: string;
  riderName: string;
}
