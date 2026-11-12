import type { EventDetail } from '@/features/events/api'
import { base64ToBytes } from '@/features/events/payload/bytes'

/** Headers curl or the connection sets itself; replaying them would be wrong. */
const HOP_BY_HOP = new Set([
  'host',
  'content-length',
  'connection',
  'transfer-encoding',
  'keep-alive',
  'te',
  'trailer',
  'upgrade',
  'proxy-connection',
])

/** POSIX single-quoting: everything is literal, and ' becomes '\''. */
export const shellQuote = (s: string) => `'${s.replaceAll("'", `'\\''`)}'`

/**
 * A printf format that reproduces `bytes` exactly. Printable ASCII is kept
 * (except \, %, and '), everything else is an octal escape, which every POSIX
 * printf understands (\x is not portable).
 */
export function printfFormat(bytes: Uint8Array): string {
  let out = ''
  for (const b of bytes) {
    const printable = b >= 0x20 && b < 0x7f && b !== 0x5c && b !== 0x25 && b !== 0x27
    out += printable ? String.fromCharCode(b) : `\\${b.toString(8).padStart(3, '0')}`
  }
  return out
}

export type CurlCommand = { command: string; notes: string[] }

/**
 * Builds a curl command that resends the event to `receivingUrl` with the same
 * method, path, query, headers (minus hop-by-hop ones), and body. Values
 * removed at receipt are sent as the literal [REDACTED].
 */
export function buildCurl(e: EventDetail, receivingUrl: string): CurlCommand {
  const notes: string[] = []
  const url = receivingUrl.replace(/\/+$/, '') + (e.path || '') + (e.raw_query ? `?${e.raw_query}` : '')
  // One flag and its value per line.
  const lines: string[] = []
  if (e.method === 'HEAD') lines.push('--head')
  else if (e.method !== 'GET' || e.body.size > 0) lines.push(`-X ${e.method}`)
  lines.push(shellQuote(url))
  for (const h of e.headers) {
    if (HOP_BY_HOP.has(h.name.toLowerCase())) continue
    lines.push(`-H ${shellQuote(`${h.name}: ${h.value}`)}`)
  }
  let prefix = ''
  if (e.body.size > 0) {
    if (e.body.encoding === 'utf8') {
      lines.push(`--data-binary ${shellQuote(e.body.text ?? '')}`)
    } else {
      prefix = `printf ${shellQuote(printfFormat(base64ToBytes(e.body.base64 ?? '')))} | `
      lines.push('--data-binary @-')
      notes.push('The body is binary, so the command pipes its exact bytes in with printf.')
    }
  }
  if (e.redacted_count > 0) {
    notes.push(
      'Values removed at receipt are sent as [REDACTED]: the original credentials are not stored, so this command does not include them.',
    )
  }
  return { command: `${prefix}curl ${lines.join(' \\\n  ')}`, notes }
}
