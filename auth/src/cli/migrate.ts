/**
 * Applies the Better Auth schema migrations (design decision 5).
 *
 *   pnpm --filter @relaylab/auth run migrate
 *
 * Connects as auth_migrator through AUTH_MIGRATION_DATABASE_URL. Run before the
 * goose migrations: app tables reference auth."user".
 */
import { tlsProblem } from '../dburl.ts'
import { createLogger } from '../logger.ts'
import { migrate } from '../migrate.ts'

const logger = createLogger(process.env.LOG_LEVEL?.trim() || 'info')
const url = process.env.AUTH_MIGRATION_DATABASE_URL?.trim()
if (!url) {
  logger.fatal('AUTH_MIGRATION_DATABASE_URL is required')
  process.exit(1)
}
if (process.env.APP_ENV?.trim().toLowerCase() === 'production') {
  const problem = tlsProblem('AUTH_MIGRATION_DATABASE_URL', url)
  if (problem) {
    logger.fatal(problem)
    process.exit(1)
  }
}

try {
  await migrate(url, logger)
} catch (err) {
  // Driver errors can include connection details; log only the class and SQLSTATE.
  logger.fatal(
    { err: { name: (err as Error).name, code: (err as { code?: string }).code } },
    'auth migration failed',
  )
  process.exit(1)
}
