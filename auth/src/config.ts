/**
 * Auth service configuration, read once from the environment.
 *
 * Secrets are wrapped in {@link Secret} so that logging or serialising the
 * config never prints them.
 */

import { tlsProblem } from './dburl.ts'

export type AppEnv = 'development' | 'test' | 'production'

export class Secret {
  readonly #value: string
  constructor(value: string) {
    this.#value = value
  }
  reveal(): string {
    return this.#value
  }
  toString(): string {
    return '[REDACTED]'
  }
  toJSON(): string {
    return '[REDACTED]'
  }
  [Symbol.for('nodejs.util.inspect.custom')](): string {
    return '[REDACTED]'
  }
}

export interface SmtpConfig {
  host: string
  port: number
  user?: string
  password?: Secret
  /** Use implicit TLS (port 465). STARTTLS is negotiated automatically otherwise. */
  secure: boolean
}

export interface Config {
  env: AppEnv
  appOrigin: string
  port: number
  logLevel: string
  databaseUrl: Secret
  betterAuthSecret: Secret
  proxySharedSecret: Secret
  signupEnabled: boolean
  requireEmailVerification: boolean
  mailFrom: string
  smtp?: SmtpConfig
  /** Rate limiting is on everywhere; only unit tests may switch it off. */
  rateLimitEnabled: boolean
}

export class ConfigError extends Error {
  readonly problems: string[]
  constructor(problems: string[]) {
    super(`invalid auth service configuration: ${problems.join('; ')}`)
    this.name = 'ConfigError'
    this.problems = problems
  }
}

type Env = Record<string, string | undefined>

const MIN_SECRET_LENGTH = 32

export function loadConfig(env: Env = process.env): Config {
  const problems: string[] = []
  const get = (name: string) => env[name]?.trim() ?? ''

  const rawEnv = (get('APP_ENV') || 'development').toLowerCase()
  if (rawEnv !== 'development' && rawEnv !== 'test' && rawEnv !== 'production') {
    problems.push('APP_ENV: must be one of development, test, production')
  }
  const appEnv = rawEnv as AppEnv
  const production = appEnv === 'production'

  let appOrigin = get('APP_ORIGIN')
  if (!appOrigin && !production) appOrigin = 'http://localhost:5173'
  if (!appOrigin) {
    problems.push('APP_ORIGIN: is required in production')
  } else {
    try {
      const u = new URL(appOrigin)
      if (u.origin !== appOrigin.replace(/\/$/, '')) {
        problems.push('APP_ORIGIN: must be an origin (scheme, host, optional port) without a path')
      } else if (production && u.protocol !== 'https:') {
        problems.push('APP_ORIGIN: must use https in production')
      }
      appOrigin = u.origin
    } catch {
      problems.push('APP_ORIGIN: must be a valid URL')
    }
  }

  const port = Number(get('AUTH_PORT') || '3001')
  if (!Number.isInteger(port) || port < 1 || port > 65535) problems.push('AUTH_PORT: must be a TCP port')

  const databaseUrl = get('AUTH_DATABASE_URL')
  if (!databaseUrl) problems.push('AUTH_DATABASE_URL: is required')
  else if (production) {
    const p = tlsProblem('AUTH_DATABASE_URL', databaseUrl)
    if (p) problems.push(p)
  }

  const betterAuthSecret = get('BETTER_AUTH_SECRET')
  if (betterAuthSecret.length < MIN_SECRET_LENGTH) {
    problems.push(`BETTER_AUTH_SECRET: must be at least ${MIN_SECRET_LENGTH} characters`)
  }
  const proxySecret = get('AUTH_PROXY_SHARED_SECRET')
  if (proxySecret.length < MIN_SECRET_LENGTH) {
    problems.push(`AUTH_PROXY_SHARED_SECRET: must be at least ${MIN_SECRET_LENGTH} characters`)
  }
  if (betterAuthSecret && betterAuthSecret === proxySecret) {
    problems.push('AUTH_PROXY_SHARED_SECRET: must differ from BETTER_AUTH_SECRET')
  }

  const signupEnabled = parseBool(get('AUTH_SIGNUP_ENABLED'), !production, 'AUTH_SIGNUP_ENABLED', problems)
  const requireEmailVerification = parseBool(
    get('AUTH_REQUIRE_EMAIL_VERIFICATION'),
    true,
    'AUTH_REQUIRE_EMAIL_VERIFICATION',
    problems,
  )
  // Public sign-up in production is a deliberate decision (D16): it needs an
  // explicit acknowledgement and email verification.
  if (production && signupEnabled) {
    if (!parseBool(get('AUTH_ALLOW_PUBLIC_SIGNUP_ACK'), false, 'AUTH_ALLOW_PUBLIC_SIGNUP_ACK', problems)) {
      problems.push(
        'AUTH_SIGNUP_ENABLED: public sign-up in production also needs AUTH_ALLOW_PUBLIC_SIGNUP_ACK=true',
      )
    }
    if (!requireEmailVerification) {
      problems.push('AUTH_REQUIRE_EMAIL_VERIFICATION: must be true in production while sign-up is enabled')
    }
  }

  let smtp: SmtpConfig | undefined
  const smtpHost = get('SMTP_HOST')
  if (smtpHost) {
    const smtpPort = Number(get('SMTP_PORT') || '587')
    if (!Number.isInteger(smtpPort) || smtpPort < 1 || smtpPort > 65535) {
      problems.push('SMTP_PORT: must be a TCP port')
    }
    const user = get('SMTP_USER')
    const password = get('SMTP_PASSWORD')
    smtp = {
      host: smtpHost,
      port: smtpPort,
      ...(user ? { user } : {}),
      ...(password ? { password: new Secret(password) } : {}),
      secure: smtpPort === 465,
    }
  }
  // Password reset is always enabled, so production cannot run without a way to send mail.
  if (production && !smtp) {
    problems.push('SMTP_HOST: is required in production (verification and password-reset emails)')
  }

  const mailFrom = get('MAIL_FROM') || 'RelayLab <no-reply@relaylab.localhost>'

  const rateLimitEnabled = parseBool(
    get('AUTH_RATE_LIMIT_ENABLED'),
    true,
    'AUTH_RATE_LIMIT_ENABLED',
    problems,
  )
  if (production && !rateLimitEnabled)
    problems.push('AUTH_RATE_LIMIT_ENABLED: cannot be disabled in production')

  if (problems.length > 0) throw new ConfigError(problems)

  return {
    env: appEnv,
    appOrigin,
    port,
    logLevel: get('LOG_LEVEL') || 'info',
    databaseUrl: new Secret(databaseUrl),
    betterAuthSecret: new Secret(betterAuthSecret),
    proxySharedSecret: new Secret(proxySecret),
    signupEnabled,
    requireEmailVerification,
    mailFrom,
    ...(smtp ? { smtp } : {}),
    rateLimitEnabled,
  }
}

function parseBool(raw: string, fallback: boolean, name: string, problems: string[]): boolean {
  if (raw === '') return fallback
  const v = raw.toLowerCase()
  if (['1', 'true', 'yes', 'on'].includes(v)) return true
  if (['0', 'false', 'no', 'off'].includes(v)) return false
  problems.push(`${name}: must be true or false`)
  return fallback
}
