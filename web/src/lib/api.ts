/** Readiness response contract of GET /readyz (platform-health spec). */
export type Readiness =
  | { status: 'ready' }
  | { status: 'not_ready'; checks: { database: 'ok' | 'fail'; migrations: 'ok' | 'fail' } }

/** Fetches API readiness. A 503 is a valid answer, not a transport error. */
export async function fetchReadiness(signal?: AbortSignal): Promise<Readiness> {
  const res = await fetch('/readyz', { signal, headers: { Accept: 'application/json' } })
  if (res.status !== 200 && res.status !== 503) {
    throw new Error(`Unexpected readiness status ${res.status}`)
  }
  return (await res.json()) as Readiness
}

/** An API error in the standard envelope: `{ error: { code, message, request_id } }`. */
export class ApiError extends Error {
  readonly status: number
  readonly code: string
  readonly requestId: string | undefined
  /** Per-field problems for 422 validation_failed. */
  readonly fields: Record<string, string> | undefined

  constructor(
    status: number,
    code: string,
    message: string,
    requestId?: string,
    fields?: Record<string, string>,
  ) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.code = code
    this.requestId = requestId
    this.fields = fields
  }
}

export function isApiError(err: unknown): err is ApiError {
  return err instanceof ApiError
}

/**
 * Calls the RelayLab API on the same origin. The session cookie is sent
 * automatically. Bodies are JSON; non-2xx responses become {@link ApiError}.
 */
export async function apiFetch<T>(path: string, init: RequestInit = {}): Promise<T> {
  const headers = new Headers(init.headers)
  headers.set('Accept', 'application/json')
  if (init.body !== undefined && !headers.has('Content-Type')) headers.set('Content-Type', 'application/json')
  const res = await fetch(path, { ...init, headers, credentials: 'same-origin' })
  if (res.ok) {
    if (res.status === 204) return undefined as T
    return (await res.json()) as T
  }
  let code = 'http_error'
  let message = 'Something went wrong. Try again.'
  let requestId: string | undefined
  let fields: Record<string, string> | undefined
  try {
    const body = (await res.json()) as {
      error?: { code?: string; message?: string; request_id?: string; fields?: Record<string, string> }
    }
    code = body.error?.code ?? code
    message = body.error?.message ?? message
    requestId = body.error?.request_id
    fields = body.error?.fields
  } catch {
    // Not an envelope (for example a proxy error page); keep the generic message.
  }
  throw new ApiError(res.status, code, message, requestId, fields)
}

/** GET /api/v1/me: the signed-in user, as established by the Go API. */
export type Me = {
  id: string
  email: string
  name: string
  email_verified: boolean
  /** A demo workspace user (anonymous, synthetic data, sink-only replay). */
  is_demo?: boolean
  /** When the demo workspace and its data are deleted (demo users only). */
  demo_expires_at?: string
}

/** GET /api/v1/config: public settings the UI needs before sign-in. */
export type PublicConfig = {
  signup_enabled: boolean
  email_verification_required: boolean
  /** REPLAY_ENABLED on the server. */
  replay_enabled?: boolean
  /** URL replay also needs a worker reporting an active outbound guard. */
  replay_url_available?: boolean
  /** How long events are kept (add-event-retention). event_ttl_seconds 0: not deleted by age. */
  retention?: { event_ttl_seconds: number; max_events_per_project: number }
  /** DEMO_ENABLED: offer "Try the demo". */
  demo_enabled?: boolean
  /** DEMO_VIDEO_URL: the product demo video; absent when not configured. */
  demo_video_url?: string
  /** PUBLIC_REPO_URL: the public repository; absent while it is private. */
  repo_url?: string
}

export const fetchMe = (signal?: AbortSignal) => apiFetch<Me>('/api/v1/me', { signal })
export const fetchPublicConfig = (signal?: AbortSignal) =>
  apiFetch<PublicConfig>('/api/v1/config', { signal })
