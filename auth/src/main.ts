import { createAuth } from './auth.ts'
import { ConfigError, loadConfig } from './config.ts'
import { checkReady, createPool } from './db.ts'
import { createLogger } from './logger.ts'
import { MemoryMailer, createSmtpMailer } from './mailer.ts'
import { startRateLimitPruner } from './retention.ts'
import { createAuthServer } from './server.ts'

const SHUTDOWN_GRACE_MS = 15_000

let config
try {
  config = loadConfig()
} catch (err) {
  const bootLogger = createLogger('info')
  bootLogger.fatal(
    { problems: err instanceof ConfigError ? err.problems : ['unknown'] },
    'invalid configuration',
  )
  process.exit(1)
}

const logger = createLogger(config.logLevel)
const pool = createPool(config.databaseUrl.reveal())
const mailer = config.smtp ? createSmtpMailer(config.smtp, config.mailFrom, logger) : new MemoryMailer(logger)
if (!config.smtp) {
  logger.warn('SMTP_HOST is not set: emails are captured in memory and not delivered (development only)')
}

const auth = createAuth({ config, pool, mailer, logger })
const server = createAuthServer({
  auth,
  proxySecret: config.proxySharedSecret.reveal(),
  logger,
  ready: () => checkReady(pool),
})

const stopPruner = startRateLimitPruner(pool, logger)

server.listen(config.port, () => {
  logger.info(
    { port: config.port, env: config.env, signup_enabled: config.signupEnabled },
    'auth service listening',
  )
})

let stopping = false
function shutdown(signal: string): void {
  if (stopping) return
  stopping = true
  logger.info({ signal }, 'shutting down')
  stopPruner()
  const force = setTimeout(() => process.exit(1), SHUTDOWN_GRACE_MS)
  force.unref()
  server.close(() => {
    pool.end().finally(() => process.exit(0))
  })
  server.closeIdleConnections()
}
process.on('SIGTERM', () => shutdown('SIGTERM'))
process.on('SIGINT', () => shutdown('SIGINT'))
