import { betterAuth, type BetterAuthOptions } from 'better-auth'
import { APIError, createAuthMiddleware } from 'better-auth/api'
import type { Pool } from 'pg'
import type { Logger } from 'pino'

import type { Config } from './config.ts'
import { scrub } from './logger.ts'
import type { Mailer } from './mailer.ts'
import {
  PASSWORD_MAX_LENGTH,
  PASSWORD_MIN_LENGTH,
  isCommonPassword,
  validateDisplayName,
} from './passwords.ts'

export const BASE_PATH = '/api/auth'
export const COOKIE_PREFIX = 'relaylab'

/** Session lifetime (spec: 7 days after last refresh, refreshed at most daily). */
export const SESSION_EXPIRES_IN = 7 * 24 * 60 * 60
export const SESSION_UPDATE_AGE = 24 * 60 * 60
export const VERIFICATION_EXPIRES_IN = 24 * 60 * 60
export const RESET_EXPIRES_IN = 60 * 60

/** Window of the default per-IP rate limit on every other auth route, in seconds. */
export const RATE_LIMIT_DEFAULT_WINDOW = 60

/** Per-IP rate limits on the endpoints that send mail or check passwords (design decision 8). */
export const RATE_LIMIT_RULES = {
  '/sign-in/email': { window: 300, max: 10 },
  '/sign-up/email': { window: 3600, max: 5 },
  '/request-password-reset': { window: 3600, max: 3 },
  '/forget-password': { window: 3600, max: 3 },
  '/send-verification-email': { window: 3600, max: 3 },
} as const

export interface AuthDeps {
  config: Config
  pool: Pool
  mailer: Mailer
  logger: Logger
}

/** Endpoints whose body carries a new password, and the field that holds it. */
const NEW_PASSWORD_FIELDS: Record<string, string> = {
  '/sign-up/email': 'password',
  '/reset-password': 'newPassword',
  '/change-password': 'newPassword',
}

function invalid(code: string, message: string): never {
  throw new APIError('BAD_REQUEST', { code, message })
}

/**
 * Better Auth options. Shared by the running service and the migrate command,
 * so the migrated schema always matches the configuration that uses it.
 */
export function authOptions({ config, pool, mailer, logger }: AuthDeps) {
  const production = config.env === 'production'

  return {
    appName: 'RelayLab',
    database: pool,
    secret: config.betterAuthSecret.reveal(),
    baseURL: config.appOrigin,
    basePath: BASE_PATH,
    trustedOrigins: [config.appOrigin],
    telemetry: { enabled: false },
    logger: {
      level: config.logLevel === 'debug' ? 'debug' : 'warn',
      // Better Auth's messages are third-party text: scrub them and drop extra arguments.
      log: (level, message) => {
        const safe = scrub(message)
        if (level === 'error') logger.error({ source: 'better-auth' }, safe)
        else if (level === 'warn') logger.warn({ source: 'better-auth' }, safe)
        else logger.debug({ source: 'better-auth' }, safe)
      },
    },
    advanced: {
      cookiePrefix: COOKIE_PREFIX,
      useSecureCookies: production,
      // Only the Go proxy sets X-Forwarded-For, to the client IP it derived from trusted proxies.
      // Do not set disableIpTracking: spike 1.1 showed it also disables rate limiting.
      ipAddress: { ipAddressHeaders: ['x-forwarded-for'] },
      // Explicit: Better Auth switches its origin/CSRF checks off by default when it detects a
      // test runner. Keeping them on everywhere means tests exercise the production behaviour.
      disableOriginCheck: false,
      disableCSRFCheck: false,
    },
    session: {
      expiresIn: SESSION_EXPIRES_IN,
      updateAge: SESSION_UPDATE_AGE,
      // Disabled so revocation is immediate; the Go API validates against auth.session on every request.
      cookieCache: { enabled: false },
    },
    emailAndPassword: {
      enabled: true,
      disableSignUp: !config.signupEnabled,
      requireEmailVerification: config.requireEmailVerification,
      minPasswordLength: PASSWORD_MIN_LENGTH,
      maxPasswordLength: PASSWORD_MAX_LENGTH,
      resetPasswordTokenExpiresIn: RESET_EXPIRES_IN,
      revokeSessionsOnPasswordReset: true,
      sendResetPassword: async ({ user, url }) => {
        await mailer.send({ to: user.email, template: 'reset-password', url, userId: user.id })
      },
    },
    emailVerification: {
      sendOnSignUp: true,
      autoSignInAfterVerification: true,
      expiresIn: VERIFICATION_EXPIRES_IN,
      sendVerificationEmail: async ({ user, url }) => {
        await mailer.send({ to: user.email, template: 'verify-email', url, userId: user.id })
      },
    },
    user: {
      deleteUser: { enabled: true },
    },
    rateLimit: {
      enabled: config.rateLimitEnabled,
      storage: 'database',
      window: RATE_LIMIT_DEFAULT_WINDOW,
      max: 100,
      customRules: { ...RATE_LIMIT_RULES },
    },
    databaseHooks: {
      session: {
        create: {
          // Privacy: never store IP address or user agent on session rows.
          before: async (session) => ({ data: { ...session, ipAddress: null, userAgent: null } }),
        },
      },
    },
    hooks: {
      before: createAuthMiddleware(async (ctx) => {
        const body = (ctx.body ?? {}) as Record<string, unknown>
        const field = NEW_PASSWORD_FIELDS[ctx.path]
        if (field) {
          const pw = body[field]
          // Length errors come from Better Auth itself; only check the list for passwords that pass them.
          const lengthOk =
            typeof pw === 'string' && pw.length >= PASSWORD_MIN_LENGTH && pw.length <= PASSWORD_MAX_LENGTH
          if (lengthOk && isCommonPassword(pw)) {
            invalid('PASSWORD_TOO_COMMON', 'This password is too common. Choose a less predictable one.')
          }
        }
        if (ctx.path === '/sign-up/email' || (ctx.path === '/update-user' && 'name' in body)) {
          const problem = validateDisplayName(body.name)
          if (problem) invalid('INVALID_NAME', problem)
        }
      }),
    },
  } satisfies BetterAuthOptions
}

export function createAuth(deps: AuthDeps) {
  return betterAuth(authOptions(deps))
}

export type Auth = ReturnType<typeof createAuth>
