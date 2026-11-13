import { afterAll, beforeAll, describe, expect, it } from 'vitest'

import { pruneExpiredDemoUsers } from '../src/retention.ts'
import { createTestDatabase, type TestDatabase } from './helpers/db.ts'
import { migrateTestDatabase, startAuth, type Harness } from './helpers/harness.ts'

let db: TestDatabase
let h: Harness

beforeAll(async () => {
  db = await createTestDatabase()
  await migrateTestDatabase(db)
  h = await startAuth(db)
})
afterAll(async () => {
  await h?.close()
  await db?.drop()
})

describe('demo workspaces (add-production-readiness 4.1, 4.6)', () => {
  it('starts a demo as an anonymous user with a session', async () => {
    const res = await h.call('/api/auth/sign-in/anonymous', { body: {}, ip: '198.51.100.20' })
    expect(res.status).toBe(200)
    expect(res.sessionCookie).toBeDefined()
    const { rows } = await h.pool.query<{ email: string; name: string; isAnonymous: boolean }>(
      `SELECT email, name, "isAnonymous" FROM auth."user" WHERE id = $1`,
      [res.body.user.id],
    )
    expect(rows[0]?.isAnonymous).toBe(true)
    expect(rows[0]?.name).toBe('Demo visitor')
    expect(rows[0]?.email).toMatch(/@demo\.relaylab\.invalid$/)
  })

  it('has no anonymous sign-in endpoint when demos are off', async () => {
    const off = await startAuth(db, { demo: { enabled: false, ttlMs: 3_600_000, startsPerHour: 10 } })
    try {
      const before = await off.pool.query(`SELECT count(*)::int AS n FROM auth."user"`)
      const res = await off.call('/api/auth/sign-in/anonymous', { body: {}, ip: '198.51.100.21' })
      expect(res.status).toBe(404)
      const after = await off.pool.query(`SELECT count(*)::int AS n FROM auth."user"`)
      expect(after.rows[0].n).toBe(before.rows[0].n)
    } finally {
      await off.close()
    }
  })

  it('limits demo starts per client IP and creates no user beyond the limit', async () => {
    const rl = await startAuth(db, {
      rateLimitEnabled: true,
      demo: { enabled: true, ttlMs: 3_600_000, startsPerHour: 2 },
    })
    try {
      const ip = '198.51.100.30'
      const count = async () =>
        (await rl.pool.query(`SELECT count(*)::int AS n FROM auth."user" WHERE "isAnonymous"`)).rows[0]
          .n as number
      const start = await count()
      expect((await rl.call('/api/auth/sign-in/anonymous', { body: {}, ip })).status).toBe(200)
      expect((await rl.call('/api/auth/sign-in/anonymous', { body: {}, ip })).status).toBe(200)
      const blocked = await rl.call('/api/auth/sign-in/anonymous', { body: {}, ip })
      expect(blocked.status).toBe(429)
      expect(Number(blocked.headers.get('x-retry-after'))).toBeGreaterThan(0)
      expect(await count()).toBe(start + 2)
      // Another address is unaffected.
      expect((await rl.call('/api/auth/sign-in/anonymous', { body: {}, ip: '198.51.100.31' })).status).toBe(
        200,
      )
    } finally {
      await rl.close()
    }
  })

  it('purges expired demo users with their sessions and keeps real users', async () => {
    const old = await h.call('/api/auth/sign-in/anonymous', { body: {}, ip: '198.51.100.40' })
    const fresh = await h.call('/api/auth/sign-in/anonymous', { body: {}, ip: '198.51.100.41' })
    const real = await h.pool.query<{ id: string }>(
      `INSERT INTO auth."user" (id, name, email, "emailVerified", "createdAt", "updatedAt")
       VALUES ('real-old-user', 'Real', 'real-old@example.test', true, now() - interval '30 days', now()) RETURNING id`,
    )
    await h.pool.query(`UPDATE auth."user" SET "createdAt" = now() - interval '25 hours' WHERE id = $1`, [
      old.body.user.id,
    ])

    const deleted = await pruneExpiredDemoUsers(h.pool, 24 * 3_600_000)
    expect(deleted).toBeGreaterThanOrEqual(1)
    const ids = (await h.pool.query<{ id: string }>(`SELECT id FROM auth."user"`)).rows.map((r) => r.id)
    expect(ids).not.toContain(old.body.user.id)
    expect(ids).toContain(fresh.body.user.id)
    expect(ids).toContain(real.rows[0]!.id)
    const sessions = await h.pool.query(`SELECT 1 FROM auth."session" WHERE "userId" = $1`, [
      old.body.user.id,
    ])
    expect(sessions.rowCount).toBe(0)
  })
})
