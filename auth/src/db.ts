import { Pool } from 'pg'

/**
 * Opens a pool on the `auth` schema. The roles also default to
 * `search_path=auth` (deploy/sql/roles.sql); setting it here keeps the service
 * correct even if a role default is missing.
 */
export function createPool(connectionString: string, max = 10): Pool {
  return new Pool({
    connectionString,
    max,
    options: '-c search_path=auth',
    connectionTimeoutMillis: 5_000,
    idleTimeoutMillis: 30_000,
    application_name: 'relaylab-auth',
  })
}

/** Readiness: the database answers and the Better Auth tables exist. */
export async function checkReady(pool: Pool): Promise<void> {
  await pool.query('SELECT 1 FROM auth."session" LIMIT 0')
}
