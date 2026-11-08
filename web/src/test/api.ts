import { vi } from 'vitest'

export type MockReply = { status?: number; body?: unknown; headers?: Record<string, string> }
export type Handler =
  MockReply | ((req: { url: URL; method: string; body: unknown }) => MockReply | Promise<MockReply>)

export type Call = { method: string; path: string; body: unknown }

/**
 * Routes fetch by "METHOD /path" (query string ignored) to canned replies, for
 * both the Go API and the Better Auth client. Unmatched requests get a 404
 * envelope. Returns the recorded calls.
 */
export function mockApi(handlers: Record<string, Handler>) {
  const calls: Call[] = []
  const spy = vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
    const req = input instanceof Request ? input : null
    const url = new URL(req ? req.url : String(input), 'http://localhost:3000')
    const method = (init?.method ?? req?.method ?? 'GET').toUpperCase()
    let body: unknown = undefined
    const raw = init?.body ?? (req ? await req.clone().text() : undefined)
    if (typeof raw === 'string' && raw) {
      try {
        body = JSON.parse(raw)
      } catch {
        body = raw
      }
    }
    calls.push({ method, path: url.pathname, body })
    const handler = handlers[`${method} ${url.pathname}`]
    const reply: MockReply = !handler
      ? { status: 404, body: { error: { code: 'not_found', message: 'Not found', request_id: 'test' } } }
      : typeof handler === 'function'
        ? await handler({ url, method, body })
        : handler
    return new Response(reply.body === undefined ? null : JSON.stringify(reply.body), {
      status: reply.status ?? 200,
      headers: { 'Content-Type': 'application/json', ...reply.headers },
    })
  })
  return { calls, spy }
}

export const me = { id: 'u1', email: 'ada@example.test', name: 'Ada Lovelace', email_verified: true }

export const signedOut = {
  'GET /api/v1/me': {
    status: 401,
    body: { error: { code: 'unauthenticated', message: 'Sign in.', request_id: 'r' } },
  },
  'GET /api/auth/get-session': { body: null },
  'GET /api/v1/config': { body: { signup_enabled: true, email_verification_required: true } },
  'GET /readyz': { body: { status: 'ready' } },
} satisfies Record<string, Handler>

export const signedIn = {
  ...signedOut,
  'GET /api/v1/me': { body: me },
  'GET /api/v1/projects': { body: { items: [] } },
  'GET /api/auth/get-session': {
    body: {
      session: { id: 's1', userId: 'u1' },
      user: { id: 'u1', email: me.email, name: me.name, emailVerified: true },
    },
  },
} satisfies Record<string, Handler>

export const apiError = (status: number, code: string) => ({
  status,
  body: { error: { code, message: code, request_id: 'r' } },
})

/** Better Auth error replies use `{ code, message }` at the top level. */
export const authError = (status: number, code: string) => ({ status, body: { code, message: code } })
