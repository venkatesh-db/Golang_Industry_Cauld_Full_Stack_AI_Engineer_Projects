import pg from 'pg';
import { config } from '../config.js';

/** Shared pg pool. One per process. */
export const pool = new pg.Pool({ connectionString: config.databaseUrl, max: 10 });

export type { PoolClient } from 'pg';
