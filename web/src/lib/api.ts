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
