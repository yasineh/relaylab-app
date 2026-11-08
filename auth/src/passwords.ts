import { dictionary } from '@zxcvbn-ts/language-common'

export const PASSWORD_MIN_LENGTH = 12
export const PASSWORD_MAX_LENGTH = 128
export const NAME_MAX_LENGTH = 80

/**
 * Common passwords: about 49k entries from the zxcvbn-ts common-password list,
 * compared case-insensitively.
 */
const COMMON: ReadonlySet<string> = new Set(
  (dictionary['passwords-common'] as string[]).map((p) => p.toLowerCase()),
)

/** Digits, punctuation, and whitespace commonly used to pad a weak password to the minimum length. */
const PADDING = /^[\d\p{P}\p{S}\s]+|[\d\p{P}\p{S}\s]+$/gu

/**
 * True if the password is on the common list, or is a common password (4+
 * characters) padded with digits or symbols, such as `Password12345!`.
 */
export function isCommonPassword(password: string): boolean {
  const lower = password.toLowerCase()
  if (COMMON.has(lower)) return true
  const core = lower.replace(PADDING, '')
  return core.length >= 4 && core !== lower && COMMON.has(core)
}

/** Display names: 1–80 characters after trimming, no control characters. */
export function validateDisplayName(name: unknown): string | null {
  if (typeof name !== 'string') return 'Display name is required.'
  const trimmed = name.trim()
  if (trimmed.length < 1) return 'Display name is required.'
  if ([...trimmed].length > NAME_MAX_LENGTH)
    return `Display name must be at most ${NAME_MAX_LENGTH} characters.`
  // eslint-disable-next-line no-control-regex
  if (/[\u0000-\u001f\u007f]/.test(trimmed)) return 'Display name must not contain control characters.'
  return null
}
