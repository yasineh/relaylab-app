import type { AddressInfo } from 'node:net'
import type { Server } from 'node:http'

import { afterAll, beforeAll, describe, expect, it } from 'vitest'

import { createAuth } from '../src/auth.ts'
import { Secret, type Config } from '../src/config.ts'
import { createPool } from '../src/db.ts'
import { createLogger } from '../src/logger.ts'
import { MemoryMailer } from '../src/mailer.ts'
import { PROXY_HEADER, createAuthServer, proxyHeaderValid } from '../src/server.ts'

const PROXY_SECRET = 'test-only-proxy-shared-secret-0123456789abcdef'

// No database: every request in this file is answered before Better Auth touches it,
// or by Better Auth's database-free /ok endpoint.
const config: Config = {
  env: 'test',
  appOrigin: 'http://relaylab.test',
  port: 0,
  logLevel: 'silent',
  databaseUrl: new Secret('postgres://nobody:nothing@127.0.0.1:1/none'),
  betterAuthSecret: new Secret('test-only-better-auth-secret-0123456789abcdef'),
  proxySharedSecret: new Secret(PROXY_SECRET),
  signupEnabled: true,
  requireEmailVerification: true,
  mailFrom: 'test@relaylab.test',
  rateLimitEnabled: false,
}

let server: Server
let base: string
let ready = true

beforeAll(async () => {
  const logger = createLogger('silent')
  const pool = createPool(config.databaseUrl.reveal(), 1)
  const auth = createAuth({ config, pool, mailer: new MemoryMailer(), logger })
  server = createAuthServer({
    auth,
    proxySecret: PROXY_SECRET,
    logger,
    ready: async () => {
      if (!ready) throw new Error('not ready')
    },
  })
  await new Promise<void>((r) => server.listen(0, '127.0.0.1', r))
  base = `http://127.0.0.1:${(server.address() as AddressInfo).port}`
})
afterAll(() => new Promise<void>((r) => server.close(() => r())))

describe('proxy header guard', () => {
  it('rejects requests without the proxy header with 403 and processes nothing', async () => {
    const res = await fetch(`${base}/api/auth/ok`)
    expect(res.status).toBe(403)
    expect(await res.json()).toEqual({ error: { code: 'forbidden', message: 'Forbidden.' } })
  })

  it('rejects a wrong proxy header', async () => {
    const res = await fetch(`${base}/api/auth/sign-in/email`, {
      method: 'POST',
      headers: { [PROXY_HEADER]: `${PROXY_SECRET}x`, 'content-type': 'application/json' },
      body: '{}',
    })
    expect(res.status).toBe(403)
  })

  it('passes requests with the correct header on to Better Auth', async () => {
    // Better Auth needs the database on its first request, and this file has none, so the
    // request fails inside Better Auth (500 from our handler) rather than at the guard.
    // Successful pass-through is covered by the integration tests.
    const res = await fetch(`${base}/api/auth/ok`, { headers: { [PROXY_HEADER]: PROXY_SECRET } })
    expect(res.status).not.toBe(403)
    expect(await res.json()).toEqual({ error: { code: 'internal', message: 'Internal error.' } })
  })

  it('compares in constant time over hashed values', () => {
    expect(proxyHeaderValid(PROXY_SECRET, PROXY_SECRET)).toBe(true)
    expect(proxyHeaderValid('short', PROXY_SECRET)).toBe(false)
    expect(proxyHeaderValid('', PROXY_SECRET)).toBe(false)
    expect(proxyHeaderValid(undefined, PROXY_SECRET)).toBe(false)
    expect(proxyHeaderValid([PROXY_SECRET, PROXY_SECRET], PROXY_SECRET)).toBe(false)
  })
})

describe('routing and limits', () => {
  it('serves liveness without the proxy header', async () => {
    const res = await fetch(`${base}/healthz`)
    expect(res.status).toBe(200)
  })

  it('reports readiness from the probe', async () => {
    expect((await fetch(`${base}/readyz`)).status).toBe(200)
    ready = false
    expect((await fetch(`${base}/readyz`)).status).toBe(503)
    ready = true
  })

  it('returns 404 outside /api/auth', async () => {
    const res = await fetch(`${base}/api/v1/me`, { headers: { [PROXY_HEADER]: PROXY_SECRET } })
    expect(res.status).toBe(404)
  })

  it('does not treat a path that only starts with the base path as an auth route', async () => {
    const res = await fetch(`${base}/api/authx/ok`, { headers: { [PROXY_HEADER]: PROXY_SECRET } })
    expect(res.status).toBe(404)
  })

  it('rejects bodies over 64 KiB with 413', async () => {
    const res = await fetch(`${base}/api/auth/sign-in/email`, {
      method: 'POST',
      headers: { [PROXY_HEADER]: PROXY_SECRET, 'content-type': 'application/json' },
      body: JSON.stringify({ email: 'a@b.test', password: 'x'.repeat(70 * 1024) }),
    })
    expect(res.status).toBe(413)
  })

  it('sends no CORS headers', async () => {
    const res = await fetch(`${base}/api/auth/ok`, {
      headers: { [PROXY_HEADER]: PROXY_SECRET, origin: 'https://evil.example' },
    })
    expect([...res.headers.keys()].filter((h) => h.startsWith('access-control-'))).toEqual([])
  })
})

describe('loggablePath', () => {
  it('redacts opaque tokens in path segments and keeps route names', async () => {
    const { loggablePath } = await import('../src/server.ts')
    expect(loggablePath('/api/auth/reset-password/4028vuIVmPNH10oq5Kq3PYtx')).toBe(
      '/api/auth/reset-password/[REDACTED]',
    )
    expect(loggablePath('/api/auth/send-verification-email')).toBe('/api/auth/send-verification-email')
    expect(loggablePath('/api/auth/request-password-reset')).toBe('/api/auth/request-password-reset')
  })
})
