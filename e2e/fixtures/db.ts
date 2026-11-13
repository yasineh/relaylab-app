import pg from 'pg'

import { DATABASE_URL } from './env.ts'

/**
 * Direct database access for situations the UI cannot produce on demand,
 * such as an expired session. It connects to the e2e stack's Postgres only
 * (DATABASE_URL points at its published test port); no other environment
 * exposes this database or these credentials.
 */
export async function withDb<T>(fn: (c: pg.Client) => Promise<T>): Promise<T> {
  const client = new pg.Client({ connectionString: DATABASE_URL })
  await client.connect()
  try {
    return await fn(client)
  } finally {
    await client.end()
  }
}

/** Expires every session of the user with this email. */
export async function expireSessions(email: string): Promise<number> {
  return withDb(async (c) => {
    const res = await c.query(
      `UPDATE auth."session" SET "expiresAt" = now() - interval '1 minute'
       WHERE "userId" = (SELECT id FROM auth."user" WHERE email = $1)`,
      [email],
    )
    return res.rowCount ?? 0
  })
}

export async function userExists(email: string): Promise<boolean> {
  return withDb(async (c) => {
    const res = await c.query(`SELECT 1 FROM auth."user" WHERE email = $1`, [email])
    return (res.rowCount ?? 0) > 0
  })
}
