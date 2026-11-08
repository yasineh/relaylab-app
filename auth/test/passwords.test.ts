import { describe, expect, it } from 'vitest'

import { isCommonPassword, validateDisplayName } from '../src/passwords.ts'
import { scrub } from '../src/logger.ts'

describe('isCommonPassword', () => {
  it.each([
    'qwerty123456',
    'QWERTY123456',
    '1qaz2wsx3edc',
    'password1234',
    'Sunshine2024!!',
    '!!!football!!!',
  ])('flags %s', (pw) => expect(isCommonPassword(pw)).toBe(true))
  it.each(['violet-anchor-89-orbit', 'correct horse battery staple', 'tR7#mQ2!vL9@xZ4'])('accepts %s', (pw) =>
    expect(isCommonPassword(pw)).toBe(false),
  )
})

describe('validateDisplayName', () => {
  it('accepts 1–80 characters after trimming', () => {
    expect(validateDisplayName('A')).toBeNull()
    expect(validateDisplayName(`  ${'é'.repeat(80)}  `)).toBeNull()
  })
  it('rejects empty, too long, non-string, and control characters', () => {
    expect(validateDisplayName('  ')).not.toBeNull()
    expect(validateDisplayName('x'.repeat(81))).not.toBeNull()
    expect(validateDisplayName(42)).not.toBeNull()
    expect(validateDisplayName('a\nb')).not.toBeNull()
  })
})

describe('scrub', () => {
  it('removes emails, URLs, and credential assignments from third-party messages', () => {
    const out = scrub(
      'Reset Password: User not found ada@example.test https://relaylab.test/reset?token=abc token=xyz password=hunter2',
    )
    expect(out).not.toMatch(/ada@example\.test|relaylab\.test|abc|xyz|hunter2/)
    expect(out).toContain('Reset Password: User not found')
  })
})
