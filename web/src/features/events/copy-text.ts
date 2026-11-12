import type { Entry, EventDetail } from '@/features/events/api'

/** Headers as "Name: value" lines. Removed values appear as [REDACTED]. */
export const headersText = (headers: Entry[]) => headers.map((h) => `${h.name}: ${h.value}`).join('\n')

/** Query parameters as "name=value" lines (decoded). Removed values appear as [REDACTED]. */
export const queryText = (query: Entry[]) => query.map((q) => `${q.name}=${q.value}`).join('\n')

/** The request line target: the path after the receiving URL plus the raw query. */
export const requestTarget = (e: Pick<EventDetail, 'path' | 'raw_query'>) =>
  (e.path || '/') + (e.raw_query ? `?${e.raw_query}` : '')

/**
 * The request as HTTP/1.1-style text, relative to the receiving URL. Binary
 * bodies are summarised rather than inlined.
 */
export function rawRequestText(e: EventDetail): string {
  const head = [`${e.method} ${requestTarget(e)} HTTP/1.1`, ...e.headers.map((h) => `${h.name}: ${h.value}`)]
  const body =
    e.body.encoding === 'utf8'
      ? (e.body.text ?? '')
      : `[binary body, ${e.body.size.toLocaleString()} bytes; download it from the Body tab]`
  return `${head.join('\n')}\n\n${body}`
}
