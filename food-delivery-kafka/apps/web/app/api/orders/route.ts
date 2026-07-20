import { NextResponse } from 'next/server';

const ORDER_SVC = process.env.ORDER_SVC_URL ?? 'http://localhost:4000';

/** Proxy: place an order via order-svc (keeps the browser off the service port). */
export async function POST(req: Request) {
  try {
    const body = await req.text();
    const res = await fetch(`${ORDER_SVC}/orders`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body,
    });
    const data = await res.json();
    return NextResponse.json(data, { status: res.status });
  } catch (err) {
    console.error('order proxy failed:', err);
    return NextResponse.json({ error: 'order-svc unreachable' }, { status: 502 });
  }
}
