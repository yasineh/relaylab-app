import type { AuthClientError } from '@/lib/auth-client'

/** Field an auth error belongs to, so forms can show it next to the input. */
export type AuthErrorField = 'email' | 'password' | 'name' | 'currentPassword' | undefined

export type MappedAuthError = { message: string; field?: AuthErrorField; code: string }

/**
 * Maps Better Auth error codes to user-facing messages. Unknown errors get a
 * generic message; raw server text is never shown, so nothing internal leaks.
 */
export function mapAuthError(error: AuthClientError): MappedAuthError {
  const code = error?.code ?? ''
  if (error?.status === 429) {
    return { code: 'RATE_LIMITED', message: 'Too many attempts. Wait a few minutes and try again.' }
  }
  switch (code) {
    case 'INVALID_EMAIL_OR_PASSWORD':
      return { code, message: 'The email or password is incorrect.' }
    case 'EMAIL_NOT_VERIFIED':
      return { code, message: 'Verify your email address before signing in.' }
    case 'PASSWORD_TOO_SHORT':
      return { code, field: 'password', message: 'Use at least 12 characters.' }
    case 'PASSWORD_TOO_LONG':
      return { code, field: 'password', message: 'Use at most 128 characters.' }
    case 'PASSWORD_TOO_COMMON':
      return {
        code,
        field: 'password',
        message: 'This password is too common. Choose a less predictable one.',
      }
    case 'INVALID_PASSWORD':
      return { code, field: 'currentPassword', message: 'The current password is incorrect.' }
    case 'INVALID_NAME':
      return { code, field: 'name', message: 'Enter a name of 1–80 characters.' }
    case 'INVALID_EMAIL':
      return { code, field: 'email', message: 'Enter a valid email address.' }
    case 'INVALID_TOKEN':
      return { code, message: 'This link is invalid or has already been used.' }
    case 'TOKEN_EXPIRED':
      return { code, message: 'This link has expired.' }
    case 'EMAIL_PASSWORD_SIGN_UP_DISABLED':
      return { code, message: 'Sign-up is not available.' }
    default:
      return { code: code || 'UNKNOWN', message: 'Something went wrong. Try again.' }
  }
}
