import { afterAll, beforeAll, describe, expect, it } from 'vitest'

import { createLogger } from '../src/logger.ts'
import { createTestDatabase, type TestDatabase } from './helpers/db.ts'
import {
  TEST_PROXY_SECRET,
  TEST_SECRET,
  migrateTestDatabase,
  startAuth,
  type Harness,
} from './helpers/harness.ts'

let db: TestDatabase
let h: Harness

beforeAll(async () => {
  db = await createTestDatabase()
  await migrateTestDatabase(db)
  // debug level: capture everything the service and Better Auth would ever log.
  h = await startAuth(db, { logLevel: 'debug' })
})
afterAll(async () => {
  await h?.close()
  await db?.drop()
})

describe('credential-safe logging', () => {
  it('logs no email, password, token, link, cookie, or secret across the auth flows', async () => {
    const email = 'Log.Capture@Example.test'
    const password = 'violet-anchor-89-orbit'
    const newPassword = 'harbor-lantern-42-quartz'
    const secrets: string[] = [
      email,
      email.toLowerCase(),
      password,
      newPassword,
      TEST_SECRET,
      TEST_PROXY_SECRET,
    ]

    // sign-up + verification email
    await h.call('/api/auth/sign-up/email', { body: { email, password, name: 'Log Capture' } })
    const verify = h.mailer.last('verify-email')!
    const verifyUrl = new URL(verify.url)
    secrets.push(verify.url, verifyUrl.searchParams.get('token')!)

    // verify (signs in)
    const verified = await h.call(verifyUrl.pathname + verifyUrl.search)
    const cookie1 = verified.sessionCookie!
    secrets.push(cookie1, decodeURIComponent(cookie1.split('=')[1]!))

    // failed and successful sign-in
    await h.call('/api/auth/sign-in/email', { body: { email, password: 'wrong-password-123' } })
    const signIn = await h.call('/api/auth/sign-in/email', { body: { email, password } })
    const cookie2 = signIn.sessionCookie!
    secrets.push(cookie2, decodeURIComponent(cookie2.split('=')[1]!))

    // reset for an unknown email (Better Auth logs "User not found") and a known one
    await h.call('/api/auth/request-password-reset', {
      body: { email: 'nobody.here@example.test', redirectTo: '/reset-password' },
    })
    secrets.push('nobody.here@example.test')
    await h.call('/api/auth/request-password-reset', { body: { email, redirectTo: '/reset-password' } })
    const reset = h.mailer.last('reset-password')!
    const resetUrl = new URL(reset.url)
    secrets.push(reset.url)
    const landing = await h.call(resetUrl.pathname + resetUrl.search)
    const token = new URL(landing.headers.get('location')!, h.config.appOrigin).searchParams.get('token')!
    secrets.push(token)
    await h.call('/api/auth/reset-password', { body: { newPassword, token } })

    // sign-out, with a stale cookie and a fresh one
    await h.call('/api/auth/sign-out', { method: 'POST', body: {}, cookie: cookie2 })
    const again = await h.call('/api/auth/sign-in/email', { body: { email, password: newPassword } })
    await h.call('/api/auth/sign-out', { method: 'POST', body: {}, cookie: again.sessionCookie! })
    secrets.push(again.sessionCookie!)

    const logs = h.logs.join('')
    expect(h.logs.length).toBeGreaterThan(10)
    for (const s of secrets) {
      expect(logs.includes(s), `log contains a credential (${s.slice(0, 6)}…)`).toBe(false)
    }
    // Request lines carry the path without its query string.
    expect(logs).toContain('"path":"/api/auth/verify-email"')
    expect(logs).toContain('"path":"/api/auth/reset-password/[REDACTED]"')
    expect(logs).not.toMatch(/token=/)
    // Users are identified by id.
    expect(logs).toMatch(/"userId":"[A-Za-z0-9]+"/)
  })
})

describe('Cloud Logging severity', () => {
  it('adds severity next to level', () => {
    const lines: string[] = []
    const log = createLogger('debug', { write: (l: string) => void lines.push(l) })
    log.debug('d')
    log.info('i')
    log.warn('w')
    log.error('e')
    expect(lines.map((l) => JSON.parse(l).severity)).toEqual(['DEBUG', 'INFO', 'WARNING', 'ERROR'])
    expect(JSON.parse(lines[0]!).level).toBe('debug')
  })
})
