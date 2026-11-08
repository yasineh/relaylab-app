import type { AddressInfo } from 'node:net'

import type { Pool } from 'pg'

import { createAuth } from '../../src/auth.ts'
import { Secret, type Config } from '../../src/config.ts'
import { checkReady, createPool } from '../../src/db.ts'
import { createLogger } from '../../src/logger.ts'
import { MemoryMailer } from '../../src/mailer.ts'
import { migrate } from '../../src/migrate.ts'
import { PROXY_HEADER, createAuthServer } from '../../src/server.ts'
import { withRoleLock, type TestDatabase } from './db.ts'

export const TEST_SECRET = 'test-only-better-auth-secret-0123456789abcdef'
export const TEST_PROXY_SECRET = 'test-only-proxy-shared-secret-0123456789abcdef'
export const TEST_ORIGIN = 'http://relaylab.test'

export function testConfig(db: TestDatabase, overrides: Partial<Config> = {}): Config {
  return {
    env: 'test',
    appOrigin: TEST_ORIGIN,
    port: 0,
    logLevel: 'debug',
    databaseUrl: new Secret(db.url('auth_runtime')),
    betterAuthSecret: new Secret(TEST_SECRET),
    proxySharedSecret: new Secret(TEST_PROXY_SECRET),
    signupEnabled: true,
    requireEmailVerification: true,
    mailFrom: 'RelayLab <no-reply@relaylab.test>',
    rateLimitEnabled: false,
    ...overrides,
  }
}

const silentLogger = createLogger('silent')

/** Migrates the auth schema of a test database as auth_migrator. */
export async function migrateTestDatabase(db: TestDatabase): Promise<void> {
  // The migrate step GRANTs to shared roles: serialise with other suites' role setup.
  await withRoleLock(() => migrate(db.url('auth_migrator'), silentLogger))
}

export interface CallOptions {
  method?: string
  body?: unknown
  cookie?: string
  headers?: Record<string, string>
  /** Client IP the Go proxy would forward. */
  ip?: string
  /** Omit the proxy header to simulate direct access. */
  direct?: boolean
}

export interface CallResult {
  status: number
  headers: Headers
  body: any
  text: string
  /** `name=value` of the session cookie, if one was set. */
  sessionCookie?: string
  setCookies: string[]
}

export interface Harness {
  baseUrl: string
  config: Config
  mailer: MemoryMailer
  pool: Pool
  logs: string[]
  call(path: string, options?: CallOptions): Promise<CallResult>
  /** Signs up (if needed), verifies through the emailed link, and returns the session cookie. */
  signUpVerified(email: string, password: string, name?: string): Promise<string>
  close(): Promise<void>
}

export async function startAuth(db: TestDatabase, overrides: Partial<Config> = {}): Promise<Harness> {
  const config = testConfig(db, overrides)
  const logs: string[] = []
  const logger = createLogger(config.logLevel, { write: (line: string) => void logs.push(line) })
  const pool = createPool(config.databaseUrl.reveal(), 5)
  const mailer = new MemoryMailer(logger)
  const auth = createAuth({ config, pool, mailer, logger })
  const server = createAuthServer({
    auth,
    proxySecret: config.proxySharedSecret.reveal(),
    logger,
    ready: () => checkReady(pool),
  })
  await new Promise<void>((resolve) => server.listen(0, '127.0.0.1', resolve))
  const baseUrl = `http://127.0.0.1:${(server.address() as AddressInfo).port}`

  async function call(path: string, o: CallOptions = {}): Promise<CallResult> {
    const headers: Record<string, string> = {
      origin: config.appOrigin,
      'x-forwarded-for': o.ip ?? '192.0.2.1',
      ...(o.direct ? {} : { [PROXY_HEADER]: config.proxySharedSecret.reveal() }),
      ...(o.body !== undefined ? { 'content-type': 'application/json' } : {}),
      ...(o.cookie ? { cookie: o.cookie } : {}),
      ...o.headers,
    }
    const res = await fetch(baseUrl + path, {
      method: o.method ?? (o.body !== undefined ? 'POST' : 'GET'),
      headers,
      redirect: 'manual',
      ...(o.body !== undefined ? { body: JSON.stringify(o.body) } : {}),
    })
    const text = await res.text()
    let body: any = null
    try {
      body = text ? JSON.parse(text) : null
    } catch {
      body = null
    }
    const setCookies = res.headers.getSetCookie()
    const session = setCookies.find((c) => /session_token=[^;]+/.test(c) && !/Max-Age=0/i.test(c))
    return {
      status: res.status,
      headers: res.headers,
      body,
      text,
      setCookies,
      ...(session ? { sessionCookie: session.split(';', 1)[0] } : {}),
    }
  }

  async function signUpVerified(email: string, password: string, name = 'Test User'): Promise<string> {
    const signUp = await call('/api/auth/sign-up/email', { body: { email, password, name } })
    if (signUp.status !== 200) throw new Error(`sign-up failed: ${signUp.status} ${signUp.text}`)
    const link = mailer.last('verify-email')
    if (!link) throw new Error('no verification email captured')
    const u = new URL(link.url)
    const verify = await call(u.pathname + u.search)
    const cookie = verify.sessionCookie
    if (!cookie) throw new Error(`verification did not sign in: ${verify.status} ${verify.text}`)
    return cookie
  }

  return {
    baseUrl,
    config,
    mailer,
    pool,
    logs,
    call,
    signUpVerified,
    async close() {
      await new Promise<void>((resolve) => server.close(() => resolve()))
      await pool.end()
    },
  }
}
