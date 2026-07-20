import { NextResponse } from 'next/server';

const ORDER_SVC = process.env.ORDER_SVC_URL ?? 'http://localhost:4000';

/** Proxy: fetch current order read-model (used for backfill-on-connect). */
export async function GET(_req: Request, { params }: { params: Promise<{ id: string }> }) {
  const { id } = await params;
  try {
    const res = await fetch(`${ORDER_SVC}/orders/${id}`, { cache: 'no-store' });
    const data = await res.json();
    return NextResponse.json(data, { status: res.status });
  } catch {
    return NextResponse.json({ error: 'order-svc unreachable' }, { status: 502 });
  }
}
