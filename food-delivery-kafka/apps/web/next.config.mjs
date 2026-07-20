/** @type {import('next').NextConfig} */
const nextConfig = {
  reactStrictMode: true,
  env: {
    ORDER_SVC_URL: process.env.ORDER_SVC_URL ?? 'http://localhost:4000',
    REDIS_URL: process.env.REDIS_URL ?? 'redis://localhost:6379',
  },
};
export default nextConfig;
