import { getMigrations } from 'better-auth/db/migration'
import type { Logger } from 'pino'

import { authOptions } from './auth.ts'
import { Secret, type Config } from './config.ts'
import { createPool } from './db.ts'
import { MemoryMailer } from './mailer.ts'

/**
 * Configuration used only to derive the schema. Secrets are not used by
 * migrations, so the migrate job does not need them (least privilege).
 */
function schemaConfig(): Config {
  const unused = new Secret('schema-only-placeholder-not-a-secret-0000')
  return {
    env: 'development',
    appOrigin: 'http://localhost',
    port: 0,
    logLevel: 'warn',
    databaseUrl: unused,
    betterAuthSecret: unused,
    proxySharedSecret: unused,
    signupEnabled: false,
    requireEmailVerification: true,
    mailFrom: 'unused@localhost',
    // Must stay true: database-backed rate limiting adds the rateLimit table.
    rateLimitEnabled: true,
    // Must stay enabled: the anonymous plugin adds user.isAnonymous, which the
    // Go API reads whether or not demos are turned on.
    demo: { enabled: true, ttlMs: 0, startsPerHour: 1 },
  }
}

/** Idempotent steps that Better Auth's own migration does not cover (design decision 5). */
const POST_STEPS = [
  // Better Auth 1.7.18 already creates session_token_key; this only guards against a future change.
  'CREATE UNIQUE INDEX IF NOT EXISTS session_token_key ON auth."session" ("token")',
  // The Go API validates sessions read-only.
  'GRANT SELECT ON auth."session", auth."user" TO app_runtime',
  // App tables reference auth."user"(id) with ON DELETE CASCADE.
  'GRANT REFERENCES ON auth."user" TO app_migrator',
]

export interface MigrationResult {
  created: string[]
  altered: string[]
}

/**
 * Applies Better Auth migrations to the `auth` schema as `auth_migrator`, then
 * the post-steps, then confirms nothing is left pending.
 */
export async function migrate(migrationUrl: string, logger: Logger): Promise<MigrationResult> {
  const pool = createPool(migrationUrl, 2)
  try {
    const options = authOptions({ config: schemaConfig(), pool, mailer: new MemoryMailer(), logger })
    const { toBeCreated, toBeAdded, runMigrations } = await getMigrations(options)
    const result = {
      created: toBeCreated.map((t) => t.table),
      altered: toBeAdded.map((t) => t.table),
    }
    await runMigrations()
    for (const sql of POST_STEPS) await pool.query(sql)

    const after = await getMigrations(options)
    if (after.toBeCreated.length > 0 || after.toBeAdded.length > 0) {
      throw new Error('auth schema still has pending changes after migrating')
    }
    logger.info(result, 'auth migrations applied')
    return result
  } finally {
    await pool.end()
  }
}
