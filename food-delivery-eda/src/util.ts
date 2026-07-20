export const sleep = (ms: number): Promise<void> => new Promise((r) => setTimeout(r, ms));

/** Random delay to simulate real-world async work (cooking, driving, gateways). */
export const jitter = (min: number, max: number): Promise<void> =>
  sleep(min + Math.floor(Math.random() * (max - min)));

export const chance = (p: number): boolean => Math.random() < p;
