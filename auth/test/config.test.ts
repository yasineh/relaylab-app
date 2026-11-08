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
    const c = loadConfig({ ...prod, AUTH_SIGNUP_ENABLED: 'true', AUTH_REQUIRE_EMAIL_VERIFICATION: 'false' })
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
})
