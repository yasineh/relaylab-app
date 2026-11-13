import type { Pool } from 'pg'
import type { Logger } from 'pino'

import { RATE_LIMIT_DEFAULT_WINDOW, RATE_LIMIT_RULES } from './auth.ts'

/**
 * auth."rateLimit" holds the only stored client IP addresses (in its key,
 * "<ip>|<path>"). A row whose last request is older than the longest
 * rate-limit window no longer limits anything: Better Auth would reset it on
 * the next request. Pruning those rows keeps IPs only while they matter
 * (add-event-retention task 2.8). The Go worker has no write access to the
 * auth schema, so the auth service, which owns it, prunes.
 */
export const RATE_LIMIT_MAX_WINDOW_SECONDS = Math.max(
  RATE_LIMIT_DEFAULT_WINDOW,
  ...Object.values(RATE_LIMIT_RULES).map((r) => r.window),
)

export const PRUNE_INTERVAL_MS = 10 * 60 * 1000
const BATCH_ROWS = 1000

/** Deletes stale rate-limit rows in batches; returns how many went. */
export async function pruneRateLimits(pool: Pool, nowMs = Date.now()): Promise<number> {
  const cutoff = nowMs - RATE_LIMIT_MAX_WINDOW_SECONDS * 1000
  let total = 0
  for (;;) {
    const res = await pool.query(
      `DELETE FROM auth."rateLimit" WHERE id IN (
         SELECT id FROM auth."rateLimit" WHERE "lastRequest" < $1 LIMIT $2)`,
      [cutoff, BATCH_ROWS],
    )
    const n = res.rowCount ?? 0
    total += n
    if (n < BATCH_ROWS) return total
  }
}

/** Prunes now and every PRUNE_INTERVAL_MS; returns a stop function. */
export function startRateLimitPruner(pool: Pool, logger: Logger, intervalMs = PRUNE_INTERVAL_MS): () => void {
  const run = () => {
    pruneRateLimits(pool).then(
      (deleted) => logger.info({ deleted }, 'rate-limit rows pruned'),
      (err: unknown) =>
        logger.error({ error: err instanceof Error ? err.message : 'unknown' }, 'rate-limit prune failed'),
    )
  }
  run()
  const timer = setInterval(run, intervalMs)
  timer.unref()
  return () => clearInterval(timer)
}
