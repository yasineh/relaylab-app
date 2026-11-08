import { createAuthClient } from 'better-auth/react'

/**
 * Better Auth client. Requests go to /api/auth on the application origin, where
 * the Go API proxies them to the auth service (same-origin; no CORS).
 *
 * The Go API, not this client, decides what a user may access: protected
 * routes check GET /api/v1/me. The client's session hook is used only to keep
 * the session refreshed (Better Auth extends it at most once a day).
 */
export const authClient = createAuthClient({
  basePath: '/api/auth',
  fetchOptions: {
    credentials: 'same-origin',
    // Resolve fetch at call time (not when this module loads) so tests can intercept it.
    customFetchImpl: (input, init) => globalThis.fetch(input, init),
  },
})

/** Error shape returned by Better Auth client calls. */
export type AuthClientError = { status?: number; code?: string; message?: string } | null | undefined
