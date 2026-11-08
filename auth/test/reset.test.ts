import { afterAll, beforeAll, beforeEach, describe, expect, it } from 'vitest'

import { render } from '../src/mailer.ts'
import { createTestDatabase, type TestDatabase } from './helpers/db.ts'
import { migrateTestDatabase, startAuth, type Harness } from './helpers/harness.ts'

const PASSWORD = 'violet-anchor-89-orbit'
const NEW_PASSWORD = 'harbor-lantern-42-quartz'

let db: TestDatabase
let h: Harness
let n = 0
const email = () => `reset${++n}-${Date.now()}@example.test`

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

const requestReset = (e: string) =>
  h.call('/api/auth/request-password-reset', { body: { email: e, redirectTo: '/reset-password' } })

/** Follows the emailed link like a browser and returns the token the reset page receives. */
async function tokenFromEmail(): Promise<string> {
  const msg = h.mailer.last('reset-password')
  if (!msg) throw new Error('no reset email')
  const link = new URL(msg.url)
  const res = await h.call(link.pathname + link.search)
  expect(res.status).toBe(302)
  const location = new URL(res.headers.get('location')!, h.config.appOrigin)
  expect(location.origin).toBe(h.config.appOrigin)
  expect(location.pathname).toBe('/reset-password')
  return location.searchParams.get('token')!
}

const meStatus = async (cookie: string) =>
  (await h.call('/api/auth/get-session', { cookie })).body === null ? 'signed-out' : 'signed-in'

describe('password reset', () => {
  it('responds identically for known and unknown emails and only mails the known one', async () => {
    const known = email()
    await h.signUpVerified(known, PASSWORD)
    h.mailer.clear()

    const a = await requestReset(known)
    const b = await requestReset(email())
    expect(a.status).toBe(200)
    expect(b.status).toBe(a.status)
    expect(b.text).toBe(a.text)
    expect(h.mailer.messages.map((m) => m.template)).toEqual(['reset-password'])
    expect(h.mailer.messages[0]?.to).toBe(known)
  })

  it('sets the new password, revokes every session, and allows sign-in with the new password', async () => {
    const e = email()
    const s1 = await h.signUpVerified(e, PASSWORD)
    const s2 = (await h.call('/api/auth/sign-in/email', { body: { email: e, password: PASSWORD } }))
      .sessionCookie!
    expect(await meStatus(s1)).toBe('signed-in')

    await requestReset(e)
    const token = await tokenFromEmail()
    const res = await h.call('/api/auth/reset-password', { body: { newPassword: NEW_PASSWORD, token } })
    expect(res.status).toBe(200)

    expect(await meStatus(s1)).toBe('signed-out')
    expect(await meStatus(s2)).toBe('signed-out')
    const { rowCount } = await h.pool.query(
      'SELECT 1 FROM auth."session" s JOIN auth."user" u ON u.id = s."userId" WHERE u.email = $1',
      [e],
    )
    expect(rowCount).toBe(0)

    const oldPw = await h.call('/api/auth/sign-in/email', { body: { email: e, password: PASSWORD } })
    expect(oldPw.status).toBe(401)
    const newPw = await h.call('/api/auth/sign-in/email', { body: { email: e, password: NEW_PASSWORD } })
    expect(newPw.status).toBe(200)
  })

  it('rejects a reused reset link and leaves the password unchanged', async () => {
    const e = email()
    await h.signUpVerified(e, PASSWORD)
    await requestReset(e)
    const token = await tokenFromEmail()
    expect(
      (await h.call('/api/auth/reset-password', { body: { newPassword: NEW_PASSWORD, token } })).status,
    ).toBe(200)

    const reuse = await h.call('/api/auth/reset-password', {
      body: { newPassword: 'another-river-55-cobalt', token },
    })
    expect(reuse.status).toBe(400)
    expect(
      (await h.call('/api/auth/sign-in/email', { body: { email: e, password: NEW_PASSWORD } })).status,
    ).toBe(200)
  })

  it('applies the sign-up password rules to the new password', async () => {
    const e = email()
    await h.signUpVerified(e, PASSWORD)
    await requestReset(e)
    const token = await tokenFromEmail()
    const short = await h.call('/api/auth/reset-password', { body: { newPassword: 'short', token } })
    expect(short.status).toBe(400)
    const common = await h.call('/api/auth/reset-password', { body: { newPassword: 'qwerty123456', token } })
    expect(common.status).toBe(400)
    expect(common.body.code).toBe('PASSWORD_TOO_COMMON')
  })

  it('stores reset tokens that expire after 1 hour', async () => {
    const e = email()
    await h.signUpVerified(e, PASSWORD)
    await requestReset(e)
    const token = await tokenFromEmail()
    const { rows } = await h.pool.query(
      `SELECT extract(epoch FROM "expiresAt" - "createdAt")::int AS ttl FROM auth.verification WHERE identifier = $1`,
      [`reset-password:${token}`],
    )
    expect(rows).toHaveLength(1)
    expect(Math.abs(rows[0].ttl - 3600)).toBeLessThan(5)
  })

  it('rejects an expired reset token', async () => {
    const e = email()
    await h.signUpVerified(e, PASSWORD)
    await requestReset(e)
    const token = await tokenFromEmail()
    await h.pool.query(
      `UPDATE auth.verification SET "expiresAt" = now() - interval '1 minute' WHERE identifier = $1`,
      [`reset-password:${token}`],
    )
    const res = await h.call('/api/auth/reset-password', { body: { newPassword: NEW_PASSWORD, token } })
    expect(res.status).toBe(400)
    expect((await h.call('/api/auth/sign-in/email', { body: { email: e, password: PASSWORD } })).status).toBe(
      200,
    )
  })
})

describe('email templates', () => {
  it('render the link in text and HTML, escaping it in HTML', () => {
    const url = 'https://relaylab.test/api/auth/verify-email?token=a"b<c>&d'
    const v = render('verify-email', url)
    expect(v.text).toContain(url)
    expect(v.html).toContain('token=a&quot;b&lt;c&gt;&amp;d')
    expect(v.html).not.toContain('a"b<c>')
    expect(render('reset-password', url).subject).toMatch(/reset/i)
  })
})
