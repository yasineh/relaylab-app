import type { Auth } from './auth.ts'
import {
  PASSWORD_MAX_LENGTH,
  PASSWORD_MIN_LENGTH,
  isCommonPassword,
  validateDisplayName,
} from './passwords.ts'

export class CreateUserError extends Error {
  constructor(message: string) {
    super(message)
    this.name = 'CreateUserError'
  }
}

export interface NewUser {
  email: string
  name: string
  password: string
}

const EMAIL_RE = /^[^\s@]+@[^\s@]+\.[^\s@]+$/

/**
 * Creates a verified email/password account directly through Better Auth's
 * internal adapter, the same way its admin plugin does. This bypasses the
 * public sign-up switch on purpose (operator CLI, D16) but applies the same
 * email, display-name, and password rules as sign-up.
 *
 * Returns the new user id.
 */
export async function createVerifiedUser(auth: Auth, input: NewUser): Promise<string> {
  const email = input.email.trim().toLowerCase()
  if (!EMAIL_RE.test(email)) throw new CreateUserError('Email address is not valid.')
  const nameProblem = validateDisplayName(input.name)
  if (nameProblem) throw new CreateUserError(nameProblem)
  const { password } = input
  if (password.length < PASSWORD_MIN_LENGTH || password.length > PASSWORD_MAX_LENGTH) {
    throw new CreateUserError(`Password must be ${PASSWORD_MIN_LENGTH}–${PASSWORD_MAX_LENGTH} characters.`)
  }
  if (isCommonPassword(password)) throw new CreateUserError('This password is too common.')

  const ctx = await auth.$context
  if (await ctx.internalAdapter.findUserByEmail(email)) {
    throw new CreateUserError('An account with this email already exists.')
  }
  const user = await ctx.internalAdapter.createUser(
    { email, name: input.name.trim(), emailVerified: true },
    { method: 'admin' },
  )
  if (!user) throw new CreateUserError('The account could not be created.')
  await ctx.internalAdapter.linkAccount({
    providerId: 'credential',
    accountId: user.id,
    userId: user.id,
    password: await ctx.password.hash(password),
  })
  return user.id
}
