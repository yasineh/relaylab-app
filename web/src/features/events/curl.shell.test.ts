// @vitest-environment node
/// <reference types="node" />
import { execFileSync } from 'node:child_process'

import { describe, expect, it } from 'vitest'

import type { EventDetail } from '@/features/events/api'
import { buildCurl } from '@/features/events/curl'

/**
 * Runs a generated command in a real POSIX shell with `curl` replaced by a
 * function that prints each argument and the stdin bytes, so quoting is
 * proven by the shell itself.
 */
function runInShell(command: string): { args: string[]; stdin: number[] } {
  const script = `
curl() { for a in "$@"; do printf '%s\\0' "$a"; done; printf '\\001'; od -An -tu1 -v; }
${command}
`
  const out = execFileSync('sh', ['-c', script], { input: '' })
  const sep = out.indexOf(1)
  const args = out.subarray(0, sep).toString('utf8').split('\0').slice(0, -1)
  const stdin = out
    .subarray(sep + 1)
    .toString('utf8')
    .trim()
    .split(/\s+/)
    .filter(Boolean)
    .map(Number)
  return { args, stdin }
}

const base: EventDetail = {
  id: 'e1',
  endpoint_id: 'ep',
  endpoint_name: 'Stripe',
  method: 'POST',
  path: "/x'$(touch /tmp/relaylab-pwned)",
  content_type: 'text/plain',
  body_size: 1,
  received_at: '2026-11-03T09:15:00Z',
  raw_query: 'q=%27&r=1',
  query: [],
  headers: [{ name: 'X-Evil', value: "'; rm -rf ~; '`id` $HOME \\n", redacted: false }],
  redacted_count: 0,
  body: {
    encoding: 'utf8',
    text: "line1\n'; echo pwned; '\n$(id)",
    is_json: false,
    json_invalid: false,
    size: 1,
  },
  source_ip_prefix: null,
  user_agent: null,
  correlation_id: 'r',
}

describe('generated curl in a real shell', () => {
  it('passes every value literally', () => {
    const { args } = runInShell(buildCurl(base, 'https://relaylab.test/h/T').command)
    expect(args).toEqual([
      '-X',
      'POST',
      "https://relaylab.test/h/T/x'$(touch /tmp/relaylab-pwned)?q=%27&r=1",
      '-H',
      "X-Evil: '; rm -rf ~; '`id` $HOME \\n",
      '--data-binary',
      "line1\n'; echo pwned; '\n$(id)",
    ])
  })

  it('pipes binary bodies byte for byte', () => {
    const bytes = [0xff, 0xfe, 0x00, 0x25, 0x5c, 0x27, 0x0a, 0x41, 0x7f, 0x80]
    const e: EventDetail = {
      ...base,
      body: {
        encoding: 'binary',
        base64: btoa(String.fromCharCode(...bytes)),
        is_json: false,
        json_invalid: false,
        size: bytes.length,
      },
    }
    const { args, stdin } = runInShell(buildCurl(e, 'https://relaylab.test/h/T').command)
    expect(args.slice(-2)).toEqual(['--data-binary', '@-'])
    expect(stdin).toEqual(bytes)
  })
})
