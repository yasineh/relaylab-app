import { z } from 'zod'

// Mirrors the server rules (auth/src/passwords.ts). The server is authoritative:
// it also rejects common passwords, which the client does not check.
export const PASSWORD_MIN = 12
export const PASSWORD_MAX = 128
export const NAME_MAX = 80

export const emailSchema = z
  .string()
  .trim()
  .min(1, 'Enter your email address.')
  .email('Enter a valid email address.')

export const newPasswordSchema = z
  .string()
  .min(PASSWORD_MIN, `Use at least ${PASSWORD_MIN} characters.`)
  .max(PASSWORD_MAX, `Use at most ${PASSWORD_MAX} characters.`)

export const nameSchema = z
  .string()
  .trim()
  .min(1, 'Enter your name.')
  .max(NAME_MAX, `Use at most ${NAME_MAX} characters.`)

export const signInSchema = z.object({
  email: emailSchema,
  password: z.string().min(1, 'Enter your password.'),
})

export const signUpSchema = z.object({
  name: nameSchema,
  email: emailSchema,
  password: newPasswordSchema,
})

export const forgotPasswordSchema = z.object({ email: emailSchema })

export const resetPasswordSchema = z
  .object({ password: newPasswordSchema, confirm: z.string() })
  .refine((v) => v.password === v.confirm, { path: ['confirm'], message: 'Passwords do not match.' })

export type SignInValues = z.infer<typeof signInSchema>
export type SignUpValues = z.infer<typeof signUpSchema>
export type ForgotPasswordValues = z.infer<typeof forgotPasswordSchema>
export type ResetPasswordValues = z.infer<typeof resetPasswordSchema>
