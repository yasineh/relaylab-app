import { inspect } from 'node:util'

import { describe, expect, it } from 'vitest'

import { ConfigError, loadConfig } from '../src/config.ts'

const base = {
  AUTH_DATABASE_URL: 'postgres://auth_runtime:x@localhost/relaylab',
  BETTER_AUTH_SECRET: 'b'.repeat(40),
  AUTH_PROXY_SHARED_SECRET: 'p'.repeat(40),
}
const prod = {
  ...base,
  AUTH_DATABASE_URL: 'postgres://auth_runtime:x@db.example.test/relaylab?sslmode=require',
  APP_ENV: 'production',
  APP_ORIGIN: 'https://relaylab.example.test',
  SMTP_HOST: 'smtp.example.test',
}

function problems(env: Record<string, string>): string[] {
  try {
    loadConfig(env)
    return []
  } catch (err) {
    if (err instanceof ConfigError) return err.problems
    throw err
  }
}

describe('loadConfig', () => {
  it('defaults sign-up on and verification on in development', () => {
    const c = loadConfig(base)
    expect(c.env).toBe('development')
    expect(c.signupEnabled).toBe(true)
    expect(c.requireEmailVerification).toBe(true)
    expect(c.rateLimitEnabled).toBe(true)
    expect(c.appOrigin).toBe('http://localhost:5173')
    expect(c.port).toBe(3001)
  })

  it('defaults sign-up off and verification on in production', () => {
    const c = loadConfig(prod)
    expect(c.signupEnabled).toBe(false)
    expect(c.requireEmailVerification).toBe(true)
  })

  it('honours explicit switches', () => {
    const c = loadConfig({ ...base, AUTH_SIGNUP_ENABLED: 'true', AUTH_REQUIRE_EMAIL_VERIFICATION: 'false' })
    expect(c.signupEnabled).toBe(true)
    expect(c.requireEmailVerification).toBe(false)
  })

  it('refuses to start in production without SMTP', () => {
    expect(problems({ ...prod, SMTP_HOST: '' })).toEqual([
      'SMTP_HOST: is required in production (verification and password-reset emails)',
    ])
  })

  it('requires an https origin in production', () => {
    expect(problems({ ...prod, APP_ORIGIN: 'http://relaylab.example.test' })).toContain(
      'APP_ORIGIN: must use https in production',
    )
    expect(problems({ ...prod, APP_ORIGIN: '' })).toContain('APP_ORIGIN: is required in production')
  })

  it('rejects an origin with a path', () => {
    expect(problems({ ...base, APP_ORIGIN: 'http://localhost:5173/app' })).toHaveLength(1)
  })

  it('requires long, distinct secrets', () => {
    expect(problems({ ...base, BETTER_AUTH_SECRET: 'short' })).toEqual([
      'BETTER_AUTH_SECRET: must be at least 32 characters',
    ])
    expect(problems({ ...base, AUTH_PROXY_SHARED_SECRET: base.BETTER_AUTH_SECRET })).toEqual([
      'AUTH_PROXY_SHARED_SECRET: must differ from BETTER_AUTH_SECRET',
    ])
  })

  it('does not allow rate limiting to be disabled in production', () => {
    expect(problems({ ...prod, AUTH_RATE_LIMIT_ENABLED: 'false' })).toEqual([
      'AUTH_RATE_LIMIT_ENABLED: cannot be disabled in production',
    ])
  })

  it('rejects malformed booleans and environments', () => {
    expect(problems({ ...base, AUTH_SIGNUP_ENABLED: 'maybe' })).toEqual([
      'AUTH_SIGNUP_ENABLED: must be true or false',
    ])
    expect(problems({ ...base, APP_ENV: 'staging' })[0]).toMatch(/^APP_ENV/)
  })

  it('never prints secrets when the config is logged or serialised', () => {
    const c = loadConfig({ ...prod, SMTP_USER: 'mailer', SMTP_PASSWORD: 's'.repeat(20) })
    const dumped = JSON.stringify(c) + inspect(c, { depth: 5 }) + String(c.betterAuthSecret)
    for (const secret of [
      base.BETTER_AUTH_SECRET,
      base.AUTH_PROXY_SHARED_SECRET,
      base.AUTH_DATABASE_URL,
      's'.repeat(20),
    ]) {
      expect(dumped).not.toContain(secret)
    }
    expect(c.betterAuthSecret.reveal()).toBe(base.BETTER_AUTH_SECRET)
  })

  it('requires a TLS database URL in production, or a Unix socket', () => {
    for (const url of [
      'postgres://auth_runtime:hunter2-secret@db.example.test/relaylab',
      'postgres://auth_runtime:hunter2-secret@db.example.test/relaylab?sslmode=disable',
      'postgres://auth_runtime:hunter2-secret@db.example.test/relaylab?sslmode=prefer',
    ]) {
      const p = problems({ ...prod, AUTH_DATABASE_URL: url })
      expect(p).toContain(
        'AUTH_DATABASE_URL: must require TLS in production: add sslmode=require (or verify-full)',
      )
      expect(p.join(' ')).not.toContain('hunter2')
    }
    expect(problems({ ...prod, AUTH_DATABASE_URL: 'postgres://u:p@/relaylab?host=/cloudsql/p:r:i' })).toEqual(
      [],
    )
    expect(problems({ ...base, AUTH_DATABASE_URL: 'postgres://u:p@localhost/relaylab' })).toEqual([])
  })

  it('needs an acknowledgement and verification for public sign-up in production', () => {
    expect(problems({ ...prod, AUTH_SIGNUP_ENABLED: 'true' })).toContain(
      'AUTH_SIGNUP_ENABLED: public sign-up in production also needs AUTH_ALLOW_PUBLIC_SIGNUP_ACK=true',
    )
    expect(
      problems({
        ...prod,
        AUTH_SIGNUP_ENABLED: 'true',
        AUTH_ALLOW_PUBLIC_SIGNUP_ACK: 'true',
        AUTH_REQUIRE_EMAIL_VERIFICATION: 'false',
      }),
    ).toContain('AUTH_REQUIRE_EMAIL_VERIFICATION: must be true in production while sign-up is enabled')
    expect(problems({ ...prod, AUTH_SIGNUP_ENABLED: 'true', AUTH_ALLOW_PUBLIC_SIGNUP_ACK: 'true' })).toEqual(
      [],
    )
  })

  it('reads demo settings: on outside production by default, off in production', () => {
    expect(loadConfig(base).demo).toEqual({ enabled: true, ttlMs: 24 * 3_600_000, startsPerHour: 10 })
    expect(loadConfig(prod).demo.enabled).toBe(false)
    const c = loadConfig({ ...prod, DEMO_ENABLED: 'true', DEMO_TTL: '90m', DEMO_STARTS_PER_HOUR: '3' })
    expect(c.demo).toEqual({ enabled: true, ttlMs: 90 * 60_000, startsPerHour: 3 })
    expect(problems({ ...base, DEMO_TTL: '1d' })).toContain(
      'DEMO_TTL: must be a duration from 1m to 168h, such as 24h',
    )
    expect(problems({ ...base, DEMO_TTL: '200h' })).toContain(
      'DEMO_TTL: must be a duration from 1m to 168h, such as 24h',
    )
    expect(problems({ ...base, DEMO_STARTS_PER_HOUR: '0' })).toContain(
      'DEMO_STARTS_PER_HOUR: must be a whole number from 1 to 1000',
    )
  })
})
