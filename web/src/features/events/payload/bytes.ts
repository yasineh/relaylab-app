/** How much of a binary body the hex view shows before offering the download. */
export const HEX_PREVIEW_BYTES = 64 * 1024

/**
 * Characters shown as visible escapes in the raw view: C0 controls except tab
 * and newline, DEL, C1 controls, and Unicode bidirectional overrides, which can
 * make received text display differently from what it contains.
 */
// oxlint-disable-next-line no-control-regex -- matching control characters is the point
const INVISIBLE = /[\u0000-\u0008\u000B-\u001F\u007F-\u009F\u061C\u200E\u200F\u202A-\u202E\u2066-\u2069]/g

export type Segment = { text: string; escape: false } | { text: string; escape: true }

/** Splits text into plain runs and visible escapes such as `\u0000`. */
export function visibleSegments(text: string): Segment[] {
  const out: Segment[] = []
  let last = 0
  for (const m of text.matchAll(INVISIBLE)) {
    const i = m.index
    if (i > last) out.push({ text: text.slice(last, i), escape: false })
    out.push({ text: escapeChar(m[0]), escape: true })
    last = i + m[0].length
  }
  if (last < text.length) out.push({ text: text.slice(last), escape: false })
  return out
}

const escapeChar = (c: string) => {
  if (c === '\r') return '\\r'
  return `\\u${c.charCodeAt(0).toString(16).padStart(4, '0')}`
}

/** Decodes standard base64 (as the API sends binary bodies) to bytes. */
export function base64ToBytes(b64: string): Uint8Array {
  const bin = atob(b64)
  const out = new Uint8Array(bin.length)
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i)
  return out
}

/**
 * Formats bytes as a classic hex dump: offset, 16 bytes in hex, and their
 * printable ASCII (others as "."). Only the first `limit` bytes are included.
 */
export function hexDump(bytes: Uint8Array, limit = HEX_PREVIEW_BYTES): { text: string; truncated: boolean } {
  const n = Math.min(bytes.length, limit)
  const lines: string[] = []
  for (let off = 0; off < n; off += 16) {
    const row = bytes.subarray(off, Math.min(off + 16, n))
    const hex: string[] = []
    let ascii = ''
    for (let i = 0; i < 16; i++) {
      const b = row[i]
      if (b === undefined) {
        hex.push('  ')
        continue
      }
      hex.push(b.toString(16).padStart(2, '0'))
      ascii += b >= 0x20 && b < 0x7f ? String.fromCharCode(b) : '.'
    }
    // Two groups of eight, like hexdump -C.
    lines.push(
      `${off.toString(16).padStart(8, '0')}  ${hex.slice(0, 8).join(' ')}  ${hex.slice(8).join(' ')}  |${ascii}|`,
    )
  }
  return { text: lines.join('\n'), truncated: bytes.length > limit }
}

/** Formats a byte count for people ("1.5 KiB"). */
export function formatBytes(n: number): string {
  if (n < 1024) return `${n} ${n === 1 ? 'byte' : 'bytes'}`
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(n < 10 * 1024 ? 1 : 0)} KiB`
  return `${(n / (1024 * 1024)).toFixed(1)} MiB`
}
