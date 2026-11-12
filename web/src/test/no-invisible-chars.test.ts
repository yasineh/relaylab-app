import { describe, expect, it } from 'vitest'

// Every source file's text. Literal bidi or control characters in source can
// make code read differently from what it does ("Trojan Source"); write them
// as \u escapes instead.
const sources = import.meta.glob<string>('/src/**/*.{ts,tsx,css}', {
  query: '?raw',
  import: 'default',
  eager: true,
})

// oxlint-disable-next-line no-control-regex -- matching control characters is the point
const INVISIBLE = new RegExp(
  '[\\u0000-\\u0008\\u000B\\u000C\\u000E-\\u001F\\u007F-\\u009F\\u061C\\u200E\\u200F\\u202A-\\u202E\\u2066-\\u2069]',
)

describe('source files', () => {
  it('contain no literal bidi or control characters', () => {
    expect(Object.keys(sources).length).toBeGreaterThan(20)
    const offenders = Object.entries(sources)
      .filter(([, text]) => INVISIBLE.test(text))
      .map(([path]) => path)
    expect(offenders).toEqual([])
  })
})
