/**
 * Production database URLs must not allow a plaintext connection: `sslmode`
 * must be require, verify-ca, or verify-full (libpq's default, prefer, falls
 * back to plaintext). A Unix socket (Cloud SQL's connector,
 * `host=/cloudsql/...`) never crosses the network and is allowed. Returns a
 * problem description, or null. Never includes the URL.
 */
export function tlsProblem(name: string, url: string): string | null {
  // A socket URL has no network host, which WHATWG URL parsing rejects.
  if (/[?&]host=(\/|%2F)/i.test(url)) return null
  let u: URL
  try {
    u = new URL(url)
  } catch {
    return `${name}: must be a postgres:// connection URL`
  }
  const mode = u.searchParams.get('sslmode')
  if (mode === 'require' || mode === 'verify-ca' || mode === 'verify-full') return null
  return `${name}: must require TLS in production: add sslmode=require (or verify-full)`
}
