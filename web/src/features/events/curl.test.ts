import { describe, expect, it } from 'vitest'

import type { EventDetail } from '@/features/events/api'
import { buildCurl, printfFormat, shellQuote } from '@/features/events/curl'

/**
 * Splits a POSIX shell command line into words the way sh would for the
 * constructs buildCurl emits: single quotes, backslash escapes outside quotes,
 * line continuations, and the pipe.
 */
function shellWords(cmd: string): string[] {
  const words: string[] = []
  let cur: string | null = null
  for (let i = 0; i < cmd.length; i++) {
    const c = cmd[i]!
    if (c === "'") {
      const end = cmd.indexOf("'", i + 1)
      if (end < 0) throw new Error('unterminated quote')
      cur = (cur ?? '') + cmd.slice(i + 1, end)
      i = end
    } else if (c === '\\') {
      const next = cmd[i + 1]
      i++
      if (next === '\n') continue // line continuation
      cur = (cur ?? '') + next
    } else if (c === ' ' || c === '\n' || c === '\t') {
      if (cur !== null) words.push(cur)
      cur = null
    } else if ('|;&$`"<>()'.includes(c)) {
      if (cur !== null) words.push(cur)
      cur = null
      words.push(`<op ${c}>`)
    } else {
      cur = (cur ?? '') + c
    }
  }
  if (cur !== null) words.push(cur)
  return words
}

/** Interprets a printf format's octal escapes back into bytes. */
function printfBytes(fmt: string): number[] {
  const out: number[] = []
  for (let i = 0; i < fmt.length; i++) {
    if (fmt[i] === '\\') {
      out.push(parseInt(fmt.slice(i + 1, i + 4), 8))
      i += 3
    } else out.push(fmt.charCodeAt(i))
  }
  return out
}

const base: EventDetail = {
  id: 'e1',
  endpoint_id: 'ep',
  endpoint_name: 'Stripe',
  method: 'POST',
  path: '/charge',
  content_type: 'application/json',
  body_size: 12,
  received_at: '2026-11-03T09:15:00Z',
  raw_query: 'a=1&b=two%20words',
  query: [],
  headers: [
    { name: 'Content-Type', value: 'application/json', redacted: false },
    { name: 'Host', value: 'relaylab.test', redacted: false },
    { name: 'Content-Length', value: '12', redacted: false },
    { name: 'Connection', value: 'keep-alive', redacted: false },
    { name: 'Authorization', value: '[REDACTED]', redacted: true },
  ],
  redacted_count: 1,
  body: { encoding: 'utf8', text: '{"ok":true}\n', is_json: true, json_invalid: false, size: 12 },
  source_ip_prefix: null,
  user_agent: null,
  correlation_id: 'r',
}

const URL = 'https://relaylab.test/h/TOKEN'

describe('buildCurl (4.3)', () => {
  it('reproduces method, URL, headers, and body, dropping hop-by-hop headers', () => {
    const { command, notes } = buildCurl(base, URL)
    expect(shellWords(command)).toEqual([
      'curl',
      '-X',
      'POST',
      'https://relaylab.test/h/TOKEN/charge?a=1&b=two%20words',
      '-H',
      'Content-Type: application/json',
      '-H',
      'Authorization: [REDACTED]',
      '--data-binary',
      '{"ok":true}\n',
    ])
    expect(notes.join(' ')).toMatch(/does not include them/)
  })

  it('passes quote-injection attempts through literally', () => {
    const evil = "'; rm -rf ~; '"
    const e: EventDetail = {
      ...base,
      path: `/x'$(id)`,
      headers: [{ name: 'X-Evil', value: evil, redacted: false }],
      body: { ...base.body, text: `${evil}\`whoami\` $HOME "q"`, size: 30 },
      redacted_count: 0,
    }
    const words = shellWords(buildCurl(e, URL).command)
    expect(words).not.toContain('<op ;>')
    expect(words).not.toContain('<op $>')
    expect(words).toContain(`X-Evil: ${evil}`)
    expect(words).toContain(`${evil}\`whoami\` $HOME "q"`)
    expect(words).toContain(`https://relaylab.test/h/TOKEN/x'$(id)?a=1&b=two%20words`)
    expect(shellQuote("it's")).toBe(`'it'\\''s'`)
  })

  it('pipes binary bodies through printf with exact bytes', () => {
    const bytes = [0xff, 0xfe, 0x00, 0x25, 0x5c, 0x27, 0x41, 0x0a]
    const e: EventDetail = {
      ...base,
      body: {
        encoding: 'binary',
        base64: btoa(String.fromCharCode(...bytes)),
        is_json: false,
        json_invalid: false,
        size: 8,
      },
    }
    const { command, notes } = buildCurl(e, URL)
    const words = shellWords(command)
    expect(words.slice(0, 3)).toEqual(['printf', printfFormat(new Uint8Array(bytes)), '<op |>'])
    expect(printfBytes(words[1]!)).toEqual(bytes)
    expect(words.slice(-2)).toEqual(['--data-binary', '@-'])
    expect(notes.join(' ')).toMatch(/binary/)
  })

  it('uses --head for HEAD and omits -X for a bodiless GET', () => {
    expect(
      shellWords(
        buildCurl({ ...base, method: 'HEAD', body: { ...base.body, size: 0, text: '' } }, URL).command,
      ).slice(0, 2),
    ).toEqual(['curl', '--head'])
    const get = shellWords(
      buildCurl(
        { ...base, method: 'GET', raw_query: '', path: '', body: { ...base.body, size: 0, text: '' } },
        `${URL}/`,
      ).command,
    )
    expect(get.slice(0, 2)).toEqual(['curl', 'https://relaylab.test/h/TOKEN'])
    expect(get).not.toContain('--data-binary')
  })
})
