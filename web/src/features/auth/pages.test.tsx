import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'

import { authError, me, mockApi, signedIn, signedOut } from '@/test/api'
import { currentPath, renderApp } from '@/test/render'

const PASSWORD = 'violet-anchor-89-orbit'

describe('sign-in', () => {
  it('validates fields with accessible errors and does not call the server', async () => {
    const { calls } = mockApi(signedOut)
    renderApp('/sign-in')
    await userEvent.click(await screen.findByRole('button', { name: 'Sign in' }))

    const email = screen.getByLabelText('Email')
    expect(email).toHaveAttribute('aria-invalid', 'true')
    expect(email).toHaveAccessibleDescription('Enter your email address.')
    expect(screen.getByLabelText('Password')).toHaveAccessibleDescription('Enter your password.')
    expect(calls.some((c) => c.path === '/api/auth/sign-in/email')).toBe(false)
  })

  it('shows one generic message for wrong credentials and clears the password', async () => {
    mockApi({ ...signedOut, 'POST /api/auth/sign-in/email': authError(401, 'INVALID_EMAIL_OR_PASSWORD') })
    renderApp('/sign-in')
    await userEvent.type(await screen.findByLabelText('Email'), 'ada@example.test')
    await userEvent.type(screen.getByLabelText('Password'), 'wrong-password')
    await userEvent.click(screen.getByRole('button', { name: 'Sign in' }))

    expect(await screen.findByRole('alert')).toHaveTextContent('The email or password is incorrect.')
    expect(screen.getByLabelText('Password')).toHaveValue('')
  })

  it('offers to resend verification for an unverified account', async () => {
    const { calls } = mockApi({
      ...signedOut,
      'POST /api/auth/sign-in/email': authError(403, 'EMAIL_NOT_VERIFIED'),
      'POST /api/auth/send-verification-email': { body: { status: true } },
    })
    renderApp('/sign-in')
    await userEvent.type(await screen.findByLabelText('Email'), 'ada@example.test')
    await userEvent.type(screen.getByLabelText('Password'), PASSWORD)
    await userEvent.click(screen.getByRole('button', { name: 'Sign in' }))

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent('Verify your email address before signing in.')
    await userEvent.click(within(alert).getByRole('button', { name: 'Resend verification email' }))
    expect(await screen.findByText(/a new link is on its way/)).toBeInTheDocument()
    expect(calls.find((c) => c.path === '/api/auth/send-verification-email')?.body).toMatchObject({
      email: 'ada@example.test',
      callbackURL: '/verify-email',
    })
  })

  it('reports rate limiting', async () => {
    mockApi({ ...signedOut, 'POST /api/auth/sign-in/email': authError(429, '') })
    renderApp('/sign-in')
    await userEvent.type(await screen.findByLabelText('Email'), 'ada@example.test')
    await userEvent.type(screen.getByLabelText('Password'), PASSWORD)
    await userEvent.click(screen.getByRole('button', { name: 'Sign in' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Too many attempts')
  })

  it('submits with the keyboard and lands on the next page', async () => {
    let signedInNow = false
    mockApi({
      ...signedOut,
      'GET /api/v1/me': () => (signedInNow ? { body: me } : signedOut['GET /api/v1/me']),
      'POST /api/auth/sign-in/email': () => {
        signedInNow = true
        return { body: { redirect: false, token: 't', user: { id: 'u1' } } }
      },
    })
    const { router } = renderApp('/sign-in?next=%2Fsettings%2Faccount')
    await userEvent.type(await screen.findByLabelText('Email'), 'ada@example.test')
    await userEvent.type(screen.getByLabelText('Password'), `${PASSWORD}{Enter}`)
    await waitFor(() => expect(currentPath(router)).toBe('/settings/account'))
  })

  it('hides the sign-up link when sign-up is disabled', async () => {
    mockApi({
      ...signedOut,
      'GET /api/v1/config': { body: { signup_enabled: false, email_verification_required: true } },
    })
    renderApp('/sign-in')
    expect(await screen.findByRole('button', { name: 'Sign in' })).toBeInTheDocument()
    await waitFor(() =>
      expect(screen.queryByRole('link', { name: /create (an )?account/i })).not.toBeInTheDocument(),
    )
  })
})

describe('sign-up', () => {
  it('does not exist when sign-up is disabled', async () => {
    mockApi({
      ...signedOut,
      'GET /api/v1/config': { body: { signup_enabled: false, email_verification_required: true } },
    })
    renderApp('/sign-up')
    expect(await screen.findByRole('heading', { name: 'Page not found' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Create account' })).not.toBeInTheDocument()
  })

  it('validates name, email, and password length before submitting', async () => {
    const { calls } = mockApi(signedOut)
    renderApp('/sign-up')
    await userEvent.type(await screen.findByLabelText('Email'), 'not-an-email')
    await userEvent.type(screen.getByLabelText('Password'), 'short123')
    await userEvent.click(screen.getByRole('button', { name: 'Create account' }))
    expect(screen.getByLabelText('Name')).toHaveAccessibleDescription('Enter your name.')
    expect(screen.getByLabelText('Email')).toHaveAccessibleDescription('Enter a valid email address.')
    expect(screen.getByLabelText('Password')).toHaveAccessibleDescription('Use at least 12 characters.')
    expect(calls.some((c) => c.path === '/api/auth/sign-up/email')).toBe(false)
  })

  it('shows a server-side password rejection on the password field', async () => {
    mockApi({ ...signedOut, 'POST /api/auth/sign-up/email': authError(400, 'PASSWORD_TOO_COMMON') })
    renderApp('/sign-up')
    await userEvent.type(await screen.findByLabelText('Name'), 'Ada')
    await userEvent.type(screen.getByLabelText('Email'), 'ada@example.test')
    await userEvent.type(screen.getByLabelText('Password'), 'qwerty123456')
    await userEvent.click(screen.getByRole('button', { name: 'Create account' }))
    expect(await screen.findByText(/too common/)).toBeInTheDocument()
    expect(screen.getByLabelText('Password')).toHaveAttribute('aria-invalid', 'true')
  })

  it('goes to "check your email" after signing up, with the address from state, not the URL', async () => {
    const { calls } = mockApi({
      ...signedOut,
      'POST /api/auth/sign-up/email': { body: { token: null, user: { id: 'u1' } } },
    })
    const { router } = renderApp('/sign-up')
    await userEvent.type(await screen.findByLabelText('Name'), 'Ada')
    await userEvent.type(screen.getByLabelText('Email'), 'ada@example.test')
    await userEvent.type(screen.getByLabelText('Password'), PASSWORD)
    await userEvent.click(screen.getByRole('button', { name: 'Create account' }))

    expect(await screen.findByRole('heading', { name: 'Check your email' })).toBeInTheDocument()
    expect(screen.getByText('ada@example.test')).toBeInTheDocument()
    expect(currentPath(router)).toBe('/check-email')
    expect(calls.find((c) => c.path === '/api/auth/sign-up/email')?.body).toMatchObject({
      name: 'Ada',
      email: 'ada@example.test',
      callbackURL: '/verify-email',
    })
  })
})

describe('email verification landing', () => {
  it('confirms success and continues into the app', async () => {
    mockApi(signedIn)
    renderApp('/verify-email')
    expect(await screen.findByText('Your email address is confirmed.')).toBeInTheDocument()
    expect(await screen.findByRole('link', { name: 'Continue to RelayLab' })).toHaveAttribute(
      'href',
      '/projects',
    )
  })

  it('explains an expired link and offers a new one', async () => {
    mockApi({ ...signedOut, 'POST /api/auth/send-verification-email': { body: { status: true } } })
    renderApp('/verify-email?error=TOKEN_EXPIRED')
    expect(await screen.findByRole('alert')).toHaveTextContent(
      'This link has expired. Your account is not verified yet.',
    )
    await userEvent.type(screen.getByLabelText('Email'), 'ada@example.test')
    await userEvent.click(screen.getByRole('button', { name: 'Send a new link' }))
    expect(await screen.findByText(/a new link is on its way/)).toBeInTheDocument()
  })
})

describe('password recovery', () => {
  it('confirms a reset request without revealing whether the account exists', async () => {
    const { calls } = mockApi({
      ...signedOut,
      'POST /api/auth/request-password-reset': { body: { status: true } },
    })
    renderApp('/forgot-password')
    await userEvent.type(await screen.findByLabelText('Email'), 'someone@example.test{Enter}')
    expect(await screen.findByText(/If an account exists for that email/)).toBeInTheDocument()
    expect(calls.find((c) => c.path === '/api/auth/request-password-reset')?.body).toMatchObject({
      email: 'someone@example.test',
      redirectTo: '/reset-password',
    })
  })

  it('rejects a missing or invalid reset token', async () => {
    mockApi(signedOut)
    renderApp('/reset-password?error=INVALID_TOKEN')
    expect(await screen.findByRole('heading', { name: "This reset link can't be used" })).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Request a new link' })).toHaveAttribute(
      'href',
      '/forgot-password',
    )
  })

  it('requires matching passwords, then signs the user out to sign in again', async () => {
    const { calls } = mockApi({ ...signedOut, 'POST /api/auth/reset-password': { body: { status: true } } })
    const { router } = renderApp('/reset-password?token=abc123')
    await userEvent.type(await screen.findByLabelText('New password'), PASSWORD)
    await userEvent.type(screen.getByLabelText('Confirm new password'), `${PASSWORD}x`)
    await userEvent.click(screen.getByRole('button', { name: 'Set new password' }))
    expect(screen.getByLabelText('Confirm new password')).toHaveAccessibleDescription(
      'Passwords do not match.',
    )

    await userEvent.clear(screen.getByLabelText('Confirm new password'))
    await userEvent.type(screen.getByLabelText('Confirm new password'), PASSWORD)
    await userEvent.click(screen.getByRole('button', { name: 'Set new password' }))
    await waitFor(() => expect(currentPath(router)).toBe('/sign-in?reason=password-reset'))
    expect(
      await screen.findByText('Your password was changed. Sign in with your new password.'),
    ).toBeInTheDocument()
    expect(calls.find((c) => c.path === '/api/auth/reset-password')?.body).toEqual({
      newPassword: PASSWORD,
      token: 'abc123',
    })
  })

  it('shows a reused link as an error', async () => {
    mockApi({ ...signedOut, 'POST /api/auth/reset-password': authError(400, 'INVALID_TOKEN') })
    renderApp('/reset-password?token=used')
    await userEvent.type(await screen.findByLabelText('New password'), PASSWORD)
    await userEvent.type(screen.getByLabelText('Confirm new password'), PASSWORD)
    await userEvent.click(screen.getByRole('button', { name: 'Set new password' }))
    expect(await screen.findByRole('alert')).toHaveTextContent(
      'This link is invalid or has already been used.',
    )
  })
})
