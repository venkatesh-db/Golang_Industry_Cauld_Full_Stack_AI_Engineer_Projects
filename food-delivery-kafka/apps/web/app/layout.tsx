import type { Metadata } from 'next';
import './globals.css';
import { TopBar } from '../components/TopBar';

export const metadata: Metadata = {
  title: 'Feastly — Order food online',
  description: 'Event-driven food delivery on Apache Kafka',
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="en">
      <body>
        <TopBar />
        {children}
      </body>
    </html>
  );
}
