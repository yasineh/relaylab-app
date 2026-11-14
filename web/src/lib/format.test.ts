import { describe, expect, it } from 'vitest'

import { formatRelative } from '@/lib/format'

describe('formatRelative', () => {
  const now = new Date('2026-11-14T12:00:00Z')

  it('formats past times', () => {
    expect(formatRelative('2026-11-14T11:57:00Z', now)).toBe('3 minutes ago')
    expect(formatRelative('2026-11-14T10:00:00Z', now)).toBe('2 hours ago')
  })

  it('reads "now" for a time just after the clock, never "in N seconds"', () => {
    // A live event that arrived after the page's clock last ticked.
    expect(formatRelative('2026-11-14T12:00:08Z', now)).toBe('now')
    expect(formatRelative('2026-11-14T12:00:00Z', now)).toBe('now')
  })
})
