import { afterAll, beforeAll, describe, expect, it } from 'vitest'

import { RATE_LIMIT_MAX_WINDOW_SECONDS, pruneRateLimits } from '../src/retention.ts'
import { createTestDatabase, type TestDatabase } from './helpers/db.ts'
import { migrateTestDatabase, startAuth, type Harness } from './helpers/harness.ts'

let db: TestDatabase
let h: Harness

beforeAll(async () => {
  db = await createTestDatabase()
  await migrateTestDatabase(db)
  h = await startAuth(db, { rateLimitEnabled: true })
})
afterAll(async () => {
  await h?.close()
  await db?.drop()
})

describe('rate-limit pruning (add-event-retention 2.8)', () => {
  it('uses the longest configured window: one hour', () => {
    expect(RATE_LIMIT_MAX_WINDOW_SECONDS).toBe(3600)
  })

  it('deletes rows past the longest window and keeps active windows', async () => {
    // A real row from a real request, as the runtime role writes it.
    const res = await h.call('/api/auth/sign-in/email', {
      body: { email: 'nobody@example.test', password: 'not-the-password-1' },
      ip: '198.51.100.10',
    })
    expect(res.status).toBe(401)
    const now = Date.now()
    const hour = 3600 * 1000
    for (const [key, last] of [
      ['198.51.100.11|/sign-in/email', now - hour - 1000], // stale
      ['198.51.100.12|/sign-up/email', now - 2 * hour], // stale
      ['198.51.100.13|/sign-up/email', now - hour + 60_000], // inside the 1 h window
    ] as const) {
      await h.pool.query(
        `INSERT INTO auth."rateLimit" (id, key, count, "lastRequest") VALUES ($1, $2, 1, $3)`,
        [crypto.randomUUID(), key, last],
      )
    }

    const deleted = await pruneRateLimits(h.pool, now)
    expect(deleted).toBe(2)
    const { rows } = await h.pool.query<{ key: string }>(`SELECT key FROM auth."rateLimit" ORDER BY key`)
    expect(rows.map((r) => r.key)).toEqual(['198.51.100.10|/sign-in/email', '198.51.100.13|/sign-up/email'])
  })

  it('runs as the auth runtime role, which owns the auth schema data', async () => {
    const { rows } = await h.pool.query<{ u: string; del: boolean }>(
      `SELECT current_user AS u, has_table_privilege('auth."rateLimit"', 'DELETE') AS del`,
    )
    expect(rows[0]).toEqual({ u: 'auth_runtime', del: true })
  })
})
