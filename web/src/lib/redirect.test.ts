import { describe, expect, it } from 'vitest'

import { DEFAULT_AFTER_SIGN_IN, safeNext, signInPath } from '@/lib/redirect'

describe('safeNext', () => {
  it.each([
    ['/projects', '/projects'],
    ['/projects/abc?tab=events#e1', '/projects/abc?tab=events#e1'],
    ['/settings/account', '/settings/account'],
  ])('keeps same-origin path %s', (input, want) => expect(safeNext(input)).toBe(want))

  it.each([
    'https://evil.example',
    '//evil.example',
    '/\\evil.example',
    '\\\\evil.example',
    'javascript:alert(1)',
    'projects',
    '/%0d%0aLocation:%20https://evil.example'.replace('%0d%0a', '\r\n'),
    '',
    null,
    undefined,
    `/${'a'.repeat(3000)}`,
  ])('falls back to the default for %s', (input) => expect(safeNext(input)).toBe(DEFAULT_AFTER_SIGN_IN))

  it('builds sign-in paths with encoded next and reason', () => {
    expect(signInPath('/projects?x=1')).toBe('/sign-in?next=%2Fprojects%3Fx%3D1')
    expect(signInPath('/projects', 'expired')).toBe('/sign-in?next=%2Fprojects&reason=expired')
  })
})
