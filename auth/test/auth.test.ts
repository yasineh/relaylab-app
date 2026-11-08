import { afterAll, beforeAll, beforeEach, describe, expect, it } from 'vitest'

import { createTestDatabase, type TestDatabase } from './helpers/db.ts'
import { migrateTestDatabase, startAuth, type Harness } from './helpers/harness.ts'

const PASSWORD = 'violet-anchor-89-orbit'

let db: TestDatabase
let h: Harness
let n = 0
const email = () => `user${++n}-${Date.now()}@example.test`

beforeAll(async () => {
  db = await createTestDatabase()
  await migrateTestDatabase(db)
  h = await startAuth(db)
})
afterAll(async () => {
  await h?.close()
  await db?.drop()
})
beforeEach(() => h.mailer.clear())

describe('sign-up', () => {
  it('creates an unverified account, sends a verification email, and issues no session', async () => {
    const e = email()
    const res = await h.call('/api/auth/sign-up/email', {
      body: { email: e, password: PASSWORD, name: 'Ada' },
    })
    expect(res.status).toBe(200)
    expect(res.body.token).toBeNull()
    expect(res.sessionCookie).toBeUndefined()
    expect(h.mailer.messages).toHaveLength(1)
    expect(h.mailer.last()?.template).toBe('verify-email')
    const { rows } = await h.pool.query('SELECT "emailVerified" FROM auth."user" WHERE email = $1', [e])
    expect(rows).toEqual([{ emailVerified: false }])
  })

  it('normalizes the email to lowercase', async () => {
    const e = email()
    await h.call('/api/auth/sign-up/email', {
      body: { email: e.toUpperCase(), password: PASSWORD, name: 'Ada' },
    })
    const { rows } = await h.pool.query('SELECT email FROM auth."user" WHERE lower(email) = $1', [e])
    expect(rows).toEqual([{ email: e }])
  })

  it.each([
    ['an 8-character password', 'short123'],
    ['a 129-character password', 'x'.repeat(129)],
  ])('rejects %s and creates no account', async (_label, password) => {
    const e = email()
    const res = await h.call('/api/auth/sign-up/email', { body: { email: e, password, name: 'Ada' } })
    expect(res.status).toBe(400)
    expect(res.body.code).toMatch(/PASSWORD_TOO_(SHORT|LONG)/)
    const { rowCount } = await h.pool.query('SELECT 1 FROM auth."user" WHERE email = $1', [e])
    expect(rowCount).toBe(0)
  })

  it.each([['qwerty123456'], ['Password1234567!'], ['iloveyou2024!!']])(
    'rejects the common password %s',
    async (password) => {
      const res = await h.call('/api/auth/sign-up/email', { body: { email: email(), password, name: 'Ada' } })
      expect(res.status).toBe(400)
      expect(res.body.code).toBe('PASSWORD_TOO_COMMON')
    },
  )

  it.each([
    ['empty', '   '],
    ['81 characters', 'n'.repeat(81)],
    ['control characters', 'Ada\u0007'],
  ])('rejects a display name that is %s', async (_label, name) => {
    const res = await h.call('/api/auth/sign-up/email', {
      body: { email: email(), password: PASSWORD, name },
    })
    expect(res.status).toBe(400)
    expect(res.body.code).toBe('INVALID_NAME')
  })

  it('accepts an 80-character display name', async () => {
    const res = await h.call('/api/auth/sign-up/email', {
      body: { email: email(), password: PASSWORD, name: 'n'.repeat(80) },
    })
    expect(res.status).toBe(200)
  })

  it('gives an existing email the same visible outcome and leaves the account unchanged', async () => {
    const e = email()
    const first = await h.call('/api/auth/sign-up/email', {
      body: { email: e, password: PASSWORD, name: 'Ada' },
    })
    const before = await h.pool.query('SELECT id, name, "updatedAt" FROM auth."user" WHERE email = $1', [e])
    h.mailer.clear()

    const again = await h.call('/api/auth/sign-up/email', {
      body: { email: e, password: 'different-pass-77-zebra', name: 'Mallory' },
    })
    expect(again.status).toBe(first.status)
    expect(Object.keys(again.body).sort()).toEqual(Object.keys(first.body).sort())
    expect(again.body.token).toBeNull()
    expect(again.sessionCookie).toBeUndefined()
    expect(h.mailer.messages).toHaveLength(0)
    const after = await h.pool.query('SELECT id, name, "updatedAt" FROM auth."user" WHERE email = $1', [e])
    expect(after.rows).toEqual(before.rows)
  })
})

describe('sign-up disabled', () => {
  it('refuses to create an account', async () => {
    const off = await startAuth(db, { signupEnabled: false })
    try {
      const e = email()
      const res = await off.call('/api/auth/sign-up/email', {
        body: { email: e, password: PASSWORD, name: 'Ada' },
      })
      expect(res.status).toBeGreaterThanOrEqual(400)
      expect(res.sessionCookie).toBeUndefined()
      const { rowCount } = await off.pool.query('SELECT 1 FROM auth."user" WHERE email = $1', [e])
      expect(rowCount).toBe(0)
    } finally {
      await off.close()
    }
  })
})

describe('email verification', () => {
  it('verifies through the emailed link and signs the user in', async () => {
    const e = email()
    const cookie = await h.signUpVerified(e, PASSWORD)
    expect(cookie).toMatch(/^relaylab\.session_token=/)
    const { rows } = await h.pool.query('SELECT "emailVerified" FROM auth."user" WHERE email = $1', [e])
    expect(rows).toEqual([{ emailVerified: true }])
  })

  it('blocks sign-in for an unverified account and issues no session', async () => {
    const e = email()
    await h.call('/api/auth/sign-up/email', { body: { email: e, password: PASSWORD, name: 'Ada' } })
    const res = await h.call('/api/auth/sign-in/email', { body: { email: e, password: PASSWORD } })
    expect(res.status).toBe(403)
    expect(res.body.code).toBe('EMAIL_NOT_VERIFIED')
    expect(res.sessionCookie).toBeUndefined()
  })

  it('lets the user request a new verification email', async () => {
    const e = email()
    await h.call('/api/auth/sign-up/email', { body: { email: e, password: PASSWORD, name: 'Ada' } })
    h.mailer.clear()
    const res = await h.call('/api/auth/send-verification-email', { body: { email: e } })
    expect(res.status).toBe(200)
    expect(h.mailer.last()?.template).toBe('verify-email')
  })

  it('rejects a verification link older than 24 hours and leaves the account unverified', async () => {
    const e = email()
    await h.call('/api/auth/sign-up/email', { body: { email: e, password: PASSWORD, name: 'Ada' } })
    const link = new URL(h.mailer.last('verify-email')!.url)
    // Verification tokens are signed JWTs with an `exp` claim; replace with one minted 25 hours ago.
    const expired = await mintExpiredVerificationToken(e)
    link.searchParams.set('token', expired)
    link.searchParams.delete('callbackURL')
    const res = await h.call(link.pathname + link.search)
    expect(res.status).toBe(401)
    expect(res.body.code).toBe('TOKEN_EXPIRED')
    expect(res.sessionCookie).toBeUndefined()
    const { rows } = await h.pool.query('SELECT "emailVerified" FROM auth."user" WHERE email = $1', [e])
    expect(rows).toEqual([{ emailVerified: false }])
  })
  it.each([
    ['expired', 'TOKEN_EXPIRED'],
    ['invalid', 'INVALID_TOKEN'],
  ])('redirects an %s link to the web page with ?error=%s', async (kind, code) => {
    const e = email()
    await h.call('/api/auth/sign-up/email', {
      body: { email: e, password: PASSWORD, name: 'Ada', callbackURL: '/verify-email' },
    })
    const link = new URL(h.mailer.last('verify-email')!.url)
    expect(link.searchParams.get('callbackURL')).toBe('/verify-email')
    link.searchParams.set('token', kind === 'expired' ? await mintExpiredVerificationToken(e) : 'not-a-token')
    const res = await h.call(link.pathname + link.search)
    expect(res.status).toBe(302)
    expect(res.headers.get('location')).toBe(`/verify-email?error=${code}`)
    expect(res.sessionCookie).toBeUndefined()
  })

  it('redirects a valid link to the web page, signed in', async () => {
    const e = email()
    await h.call('/api/auth/sign-up/email', {
      body: { email: e, password: PASSWORD, name: 'Ada', callbackURL: '/verify-email' },
    })
    const link = new URL(h.mailer.last('verify-email')!.url)
    const res = await h.call(link.pathname + link.search)
    expect(res.status).toBe(302)
    expect(res.headers.get('location')).toBe('/verify-email')
    expect(res.sessionCookie).toBeDefined()
  })
})

describe('sign-in', () => {
  it('issues a session cookie for valid credentials', async () => {
    const e = email()
    await h.signUpVerified(e, PASSWORD)
    const res = await h.call('/api/auth/sign-in/email', { body: { email: e, password: PASSWORD } })
    expect(res.status).toBe(200)
    expect(res.sessionCookie).toMatch(/^relaylab\.session_token=/)
  })

  it('returns the same error for a wrong password and an unknown email', async () => {
    const e = email()
    await h.signUpVerified(e, PASSWORD)
    const wrong = await h.call('/api/auth/sign-in/email', {
      body: { email: e, password: 'not-the-password-1' },
    })
    const unknown = await h.call('/api/auth/sign-in/email', {
      body: { email: email(), password: 'not-the-password-1' },
    })
    expect(wrong.status).toBe(401)
    expect(unknown.status).toBe(wrong.status)
    expect(unknown.text).toBe(wrong.text)
    expect(wrong.sessionCookie).toBeUndefined()
    expect(unknown.sessionCookie).toBeUndefined()
  })
})

describe('sessions', () => {
  it('sets HttpOnly, SameSite=Lax, Path=/ and no Domain outside production', async () => {
    const e = email()
    await h.signUpVerified(e, PASSWORD)
    const res = await h.call('/api/auth/sign-in/email', { body: { email: e, password: PASSWORD } })
    const cookie = res.setCookies.find((c) => c.startsWith('relaylab.session_token='))!
    expect(cookie).toMatch(/; HttpOnly/i)
    expect(cookie).toMatch(/; SameSite=Lax/i)
    expect(cookie).toMatch(/; Path=\//i)
    expect(cookie).not.toMatch(/Domain=/i)
    expect(cookie).not.toMatch(/; Secure/i)
  })

  it('stores sessions that expire after 7 days, without IP address or user agent', async () => {
    const e = email()
    await h.signUpVerified(e, PASSWORD)
    await h.call('/api/auth/sign-in/email', {
      body: { email: e, password: PASSWORD },
      headers: { 'user-agent': 'SessionTest/1.0' },
    })
    const { rows } = await h.pool.query(
      `SELECT s."ipAddress", s."userAgent", extract(epoch FROM s."expiresAt" - s."createdAt")::int AS ttl
         FROM auth."session" s JOIN auth."user" u ON u.id = s."userId" WHERE u.email = $1`,
      [e],
    )
    expect(rows.length).toBeGreaterThan(0)
    for (const r of rows) {
      expect(r.ipAddress).toBeNull()
      expect(r.userAgent).toBeNull()
      expect(Math.abs(r.ttl - 7 * 24 * 3600)).toBeLessThan(5)
    }
  })

  it('sign-out deletes the session row and clears the cookie', async () => {
    const e = email()
    const cookie = await h.signUpVerified(e, PASSWORD)
    const token = decodeURIComponent(cookie.split('=')[1]!).split('.')[0]
    const out = await h.call('/api/auth/sign-out', { method: 'POST', body: {}, cookie })
    expect(out.status).toBe(200)
    expect(out.setCookies.some((c) => /session_token=;/.test(c) && /Max-Age=0/i.test(c))).toBe(true)
    const { rowCount } = await h.pool.query('SELECT 1 FROM auth."session" WHERE token = $1', [token])
    expect(rowCount).toBe(0)
  })

  it('sign-out without a session still succeeds', async () => {
    const res = await h.call('/api/auth/sign-out', { method: 'POST', body: {} })
    expect(res.status).toBe(200)
  })

  it('rejects a sign-out from another origin', async () => {
    const cookie = await h.signUpVerified(email(), PASSWORD)
    const res = await h.call('/api/auth/sign-out', {
      method: 'POST',
      body: {},
      cookie,
      headers: { origin: 'https://evil.example' },
    })
    expect(res.status).toBe(403)
  })
})

describe('production cookie', () => {
  it('uses the __Secure- prefix and the Secure attribute', async () => {
    const prod = await startAuth(db, { env: 'production', appOrigin: 'https://relaylab.example.test' })
    try {
      const e = email()
      await prod.signUpVerified(e, PASSWORD)
      const res = await prod.call('/api/auth/sign-in/email', { body: { email: e, password: PASSWORD } })
      const cookie = res.setCookies.find((c) => c.includes('session_token='))!
      expect(cookie).toMatch(/^__Secure-relaylab\.session_token=/)
      expect(cookie).toMatch(/; Secure/i)
      expect(cookie).toMatch(/; HttpOnly/i)
      expect(cookie).toMatch(/; SameSite=Lax/i)
      expect(cookie).toMatch(/; Path=\//i)
      expect(cookie).not.toMatch(/Domain=/i)
    } finally {
      await prod.close()
    }
  })
})

describe('rate limiting', () => {
  it('throttles the 11th failed sign-in from one IP within 5 minutes, per IP, in the database', async () => {
    const rl = await startAuth(db, { rateLimitEnabled: true })
    try {
      const body = { email: email(), password: 'not-the-password-1' }
      const ip = '198.51.100.77'
      for (let i = 0; i < 10; i++) {
        expect((await rl.call('/api/auth/sign-in/email', { body, ip })).status).toBe(401)
      }
      const blocked = await rl.call('/api/auth/sign-in/email', { body, ip })
      expect(blocked.status).toBe(429)
      // Better Auth sends X-Retry-After; the Go proxy maps it to Retry-After (design, spike 1.1).
      expect(Number(blocked.headers.get('x-retry-after'))).toBeGreaterThan(0)

      expect((await rl.call('/api/auth/sign-in/email', { body, ip: '198.51.100.78' })).status).toBe(401)
      const { rows } = await rl.pool.query(`SELECT count FROM auth."rateLimit" WHERE key = $1`, [
        `${ip}|/sign-in/email`,
      ])
      expect(rows[0]?.count).toBeGreaterThanOrEqual(10)
    } finally {
      await rl.close()
    }
  })

  it('limits verification emails to 3 per hour per IP', async () => {
    const rl = await startAuth(db, { rateLimitEnabled: true })
    try {
      const ip = '198.51.100.90'
      const statuses = []
      for (let i = 0; i < 4; i++) {
        statuses.push(
          (await rl.call('/api/auth/send-verification-email', { body: { email: email() }, ip })).status,
        )
      }
      expect(statuses).toEqual([200, 200, 200, 429])
    } finally {
      await rl.close()
    }
  })
})

/** Mints a verification token for `email` whose `exp` is one hour in the past (iat 25 h ago). */
async function mintExpiredVerificationToken(e: string): Promise<string> {
  const { SignJWT } = await import('jose')
  const now = Math.floor(Date.now() / 1000)
  return new SignJWT({ email: e })
    .setProtectedHeader({ alg: 'HS256' })
    .setIssuedAt(now - 25 * 3600)
    .setExpirationTime(now - 3600)
    .sign(new TextEncoder().encode(h.config.betterAuthSecret.reveal()))
}
