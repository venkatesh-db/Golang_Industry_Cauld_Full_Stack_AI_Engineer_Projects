import Redis from 'ioredis';

export const runtime = 'nodejs';
export const dynamic = 'force-dynamic';

const REDIS_URL = process.env.REDIS_URL ?? 'redis://localhost:6379';

/**
 * SSE endpoint. Subscribes to the Redis channel `order:{id}` that the
 * sse-gateway publishes to (Kafka → Redis → here → browser). One dedicated
 * subscriber connection per client; torn down when the client disconnects.
 */
export async function GET(req: Request) {
  const url = new URL(req.url);
  const orderId = url.searchParams.get('orderId');
  if (!orderId) return new Response('orderId required', { status: 400 });

  const channel = `order:${orderId}`;
  const sub = new Redis(REDIS_URL);
  const encoder = new TextEncoder();

  const stream = new ReadableStream({
    async start(controller) {
      controller.enqueue(encoder.encode(`event: hello\ndata: "connected"\n\n`));
      await sub.subscribe(channel);
      sub.on('message', (_ch, message) => {
        controller.enqueue(encoder.encode(`data: ${message}\n\n`));
      });
      // Heartbeat so proxies don't drop the idle connection.
      const hb = setInterval(() => controller.enqueue(encoder.encode(`: ping\n\n`)), 15000);
      req.signal.addEventListener('abort', () => {
        clearInterval(hb);
        sub.disconnect();
        controller.close();
      });
    },
    cancel() {
      sub.disconnect();
    },
  });

  return new Response(stream, {
    headers: {
      'Content-Type': 'text/event-stream',
      'Cache-Control': 'no-cache, no-transform',
      Connection: 'keep-alive',
    },
  });
}
