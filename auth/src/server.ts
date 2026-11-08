import { createHash, timingSafeEqual } from 'node:crypto'
import { createServer, type IncomingMessage, type Server, type ServerResponse } from 'node:http'

import { toNodeHandler } from 'better-auth/node'
import type { Logger } from 'pino'

import { BASE_PATH, type Auth } from './auth.ts'

export const PROXY_HEADER = 'x-relaylab-proxy'
/** Matches the Go proxy's body limit (LIMIT_API_MAX_BODY_BYTES default). */
export const MAX_BODY_BYTES = 64 * 1024

export interface ServerDeps {
  auth: Auth
  proxySecret: string
  logger: Logger
  /** Readiness probe: resolves if the database is reachable and migrated. */
  ready: () => Promise<void>
}

const digest = (v: string) => createHash('sha256').update(v, 'utf8').digest()

/**
 * Constant-time check of the shared proxy header. Both sides are hashed first
 * so the comparison does not leak the secret's length.
 */
export function proxyHeaderValid(header: string | string[] | undefined, secret: string): boolean {
  if (typeof header !== 'string' || header === '') return false
  return timingSafeEqual(digest(header), digest(secret))
}

function sendJSON(res: ServerResponse, status: number, body: unknown): void {
  const payload = JSON.stringify(body)
  res.writeHead(status, {
    'content-type': 'application/json; charset=utf-8',
    'content-length': Buffer.byteLength(payload),
    'cache-control': 'no-store',
  })
  res.end(payload)
}

/** Path without the query string: verification links carry tokens in the query. */
const pathOf = (req: IncomingMessage) => (req.url ?? '/').split('?', 1)[0] ?? '/'

/**
 * Path safe to log. Reset links carry their token as a path segment
 * (`/api/auth/reset-password/<token>`), so any long opaque segment is redacted.
 */
export function loggablePath(path: string): string {
  return path
    .split('/')
    .map((seg) => (/^[A-Za-z0-9_-]{16,}$/.test(seg) && !/^[a-z-]+$/.test(seg) ? '[REDACTED]' : seg))
    .join('/')
}

export function createAuthServer({ auth, proxySecret, logger, ready }: ServerDeps): Server {
  const handle = toNodeHandler(auth)

  const server = createServer(async (req, res) => {
    const started = process.hrtime.bigint()
    const path = pathOf(req)
    res.on('finish', () => {
      const ms = Number(process.hrtime.bigint() - started) / 1e6
      // Method, path, status, and duration only: no headers, query, or body.
      // Health probes run every few seconds, so they are logged at debug level.
      const level = path === '/healthz' || path === '/readyz' ? 'debug' : 'info'
      logger[level](
        { method: req.method, path: loggablePath(path), status: res.statusCode, duration_ms: Math.round(ms) },
        'request',
      )
    })

    try {
      if (path === '/healthz') return sendJSON(res, 200, { status: 'ok' })
      if (path === '/readyz') {
        try {
          await ready()
          return sendJSON(res, 200, { status: 'ready' })
        } catch {
          return sendJSON(res, 503, { status: 'unavailable' })
        }
      }
      if (path !== BASE_PATH && !path.startsWith(`${BASE_PATH}/`)) {
        return sendJSON(res, 404, { error: { code: 'not_found', message: 'Not found.' } })
      }
      // Defence in depth: only the Go API proxy knows this secret.
      if (!proxyHeaderValid(req.headers[PROXY_HEADER], proxySecret)) {
        return sendJSON(res, 403, { error: { code: 'forbidden', message: 'Forbidden.' } })
      }
      delete req.headers[PROXY_HEADER]
      const length = Number(req.headers['content-length'] ?? '0')
      if (Number.isNaN(length) || length > MAX_BODY_BYTES) {
        return sendJSON(res, 413, {
          error: { code: 'payload_too_large', message: 'Request body too large.' },
        })
      }
      await handle(req, res)
    } catch (err) {
      logger.error({ err: { name: (err as Error).name } }, 'unhandled auth request error')
      if (!res.headersSent) sendJSON(res, 500, { error: { code: 'internal', message: 'Internal error.' } })
      else res.destroy()
    }
  })
  server.requestTimeout = 15_000
  server.headersTimeout = 10_000
  return server
}
