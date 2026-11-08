/** Where signed-in users land when there is no (valid) `next` parameter. */
export const DEFAULT_AFTER_SIGN_IN = '/projects'

/**
 * Returns `next` only if it is a same-origin relative path: it must start with a
 * single `/`, must not start with `//` or `/\`, and must not contain control
 * characters or a scheme. Anything else falls back to the default, so the
 * sign-in page cannot be used as an open redirect.
 */
export function safeNext(next: string | null | undefined, fallback = DEFAULT_AFTER_SIGN_IN): string {
  if (!next || next.length > 2048) return fallback
  if (!next.startsWith('/') || next.startsWith('//') || next.startsWith('/\\')) return fallback
  // eslint-disable-next-line no-control-regex
  if (/[\u0000-\u001f\u007f\\]/.test(next)) return fallback
  try {
    const url = new URL(next, 'https://relaylab.invalid')
    if (url.origin !== 'https://relaylab.invalid') return fallback
    return url.pathname + url.search + url.hash
  } catch {
    return fallback
  }
}

/** `/sign-in?next=…` for the given location, with an optional reason notice. */
export function signInPath(next: string, reason?: 'expired' | 'signed-out'): string {
  const params = new URLSearchParams({ next })
  if (reason) params.set('reason', reason)
  return `/sign-in?${params.toString()}`
}
