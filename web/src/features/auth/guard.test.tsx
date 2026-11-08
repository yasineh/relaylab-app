import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'

import { apiError, me, mockApi, signedIn, signedOut } from '@/test/api'
import { currentPath, renderApp } from '@/test/render'

const PASSWORD = 'violet-anchor-89-orbit'

/** Signed out until the sign-in endpoint is called. */
function signInFlow() {
  let signedInNow = false
  return mockApi({
    ...signedOut,
    'GET /api/v1/me': () => (signedInNow ? { body: me } : signedOut['GET /api/v1/me']),
    'GET /api/auth/get-session': () => (signedInNow ? signedIn['GET /api/auth/get-session'] : { body: null }),
    'POST /api/auth/sign-in/email': () => {
      signedInNow = true
      return { body: { redirect: false, token: 't', user: { id: 'u1' } } }
    },
  })
}

async function signIn() {
  await userEvent.type(await screen.findByLabelText('Email'), 'ada@example.test')
  await userEvent.type(screen.getByLabelText('Password'), PASSWORD)
  await userEvent.click(screen.getByRole('button', { name: 'Sign in' }))
}

describe('RequireAuth', () => {
  it('redirects a signed-out deep link to sign-in and returns there afterwards', async () => {
    signInFlow()
    const { router } = renderApp('/projects')
    await waitFor(() => expect(currentPath(router)).toBe('/sign-in?next=%2Fprojects'))
    // A first visit while signed out is not an "expired session".
    expect(screen.queryByText(/session has expired/)).not.toBeInTheDocument()

    await signIn()
    await waitFor(() => expect(currentPath(router)).toBe('/projects'))
    expect(await screen.findByRole('heading', { name: 'Projects' })).toBeInTheDocument()
  })

  it('keeps query strings in next', async () => {
    signInFlow()
    const { router } = renderApp('/settings/account?tab=security')
    await waitFor(() =>
      expect(currentPath(router)).toBe('/sign-in?next=%2Fsettings%2Faccount%3Ftab%3Dsecurity'),
    )
  })

  it.each(['https://evil.example', '//evil.example', '/\\evil.example', 'javascript:alert(1)'])(
    'ignores the open-redirect attempt next=%s and lands on /projects',
    async (next) => {
      signInFlow()
      const { router } = renderApp(`/sign-in?next=${encodeURIComponent(next)}`)
      await signIn()
      await waitFor(() => expect(currentPath(router)).toBe('/projects'))
    },
  )

  it('sends an already signed-in visitor away from the sign-in page', async () => {
    mockApi(signedIn)
    const { router } = renderApp('/sign-in?next=%2Fsettings%2Faccount')
    await waitFor(() => expect(currentPath(router)).toBe('/settings/account'))
  })

  it('shows the verify-your-email state for an unverified user', async () => {
    mockApi({
      ...signedIn,
      'GET /api/v1/me': apiError(403, 'email_not_verified'),
      'POST /api/auth/send-verification-email': { body: { status: true } },
    })
    renderApp('/projects')
    expect(await screen.findByRole('heading', { name: 'Verify your email to continue' })).toBeInTheDocument()
    expect(screen.queryByRole('heading', { name: 'Projects' })).not.toBeInTheDocument()
    await userEvent.click(await screen.findByRole('button', { name: 'Resend verification email' }))
    expect(await screen.findByText(/a new link is on its way/)).toBeInTheDocument()
  })

  it('shows a retryable error when the API is unavailable', async () => {
    let fail = true
    mockApi({
      ...signedIn,
      'GET /api/v1/me': () => (fail ? apiError(500, 'internal_error') : { body: me }),
    })
    renderApp('/projects')
    expect(await screen.findByRole('alert')).toHaveTextContent('could not load your account')
    fail = false
    await userEvent.click(screen.getByRole('button', { name: 'Try again' }))
    expect(await screen.findByRole('heading', { name: 'Projects' })).toBeInTheDocument()
  })

  it('signs out from the header and returns to sign-in', async () => {
    let signedInNow = true
    const { calls } = mockApi({
      ...signedIn,
      'GET /api/v1/me': () => (signedInNow ? { body: me } : signedOut['GET /api/v1/me']),
      'POST /api/auth/sign-out': () => {
        signedInNow = false
        return { body: { success: true } }
      },
    })
    const { router } = renderApp('/projects')
    await userEvent.click(await screen.findByRole('button', { name: 'Sign out' }))
    await waitFor(() => expect(currentPath(router)).toBe('/sign-in?reason=signed-out'))
    expect(await screen.findByText('You have been signed out.')).toBeInTheDocument()
    expect(calls.some((c) => c.method === 'POST' && c.path === '/api/auth/sign-out')).toBe(true)
  })
})
