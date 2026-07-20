import { bootstrap } from './bootstrap.js';
import { startServer } from './api/server.js';

const app = bootstrap();

// Log every event as it flows through the bus — a live trace of the stream.
app.bus.onAny((e) => {
  console.log(`📨 ${e.timestamp.slice(11, 23)}  ${e.type.padEnd(22)} order=${e.orderId.slice(0, 8)}`);
});

startServer(app, Number(process.env.PORT) || 3000);
