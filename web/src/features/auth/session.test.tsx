import { screen, waitFor } from '@testing-library/react'
import { describe, expect, it } from 'vitest'

import { meQueryKey } from '@/hooks/session'
import { apiError, mockApi, signedIn } from '@/test/api'
import { currentPath, renderApp } from '@/test/render'

describe('global 401 handling', () => {
  it('navigates to sign-in with a "session expired" notice when an API call returns session_expired', async () => {
    let expired = false
    mockApi({
      ...signedIn,
      'GET /api/v1/me': () => (expired ? apiError(401, 'session_expired') : signedIn['GET /api/v1/me']),
    })
    const { router, client } = renderApp('/projects')
    expect(await screen.findByRole('heading', { name: 'Projects' })).toBeInTheDocument()

    expired = true
    await client.invalidateQueries({ queryKey: meQueryKey })

    await waitFor(() => expect(router.state.location.pathname).toBe('/sign-in'))
    expect(currentPath(router)).toBe('/sign-in?next=%2Fprojects&reason=expired')
    expect(
      await screen.findByText('Your session has expired. Sign in again to continue.'),
    ).toBeInTheDocument()
  })

  it('treats a revoked session mid-use the same way', async () => {
    let revoked = false
    mockApi({
      ...signedIn,
      'GET /api/v1/me': () => (revoked ? apiError(401, 'unauthenticated') : signedIn['GET /api/v1/me']),
    })
    const { router, client } = renderApp('/settings/account')
    expect(await screen.findByRole('heading', { name: 'Account settings' })).toBeInTheDocument()

    revoked = true
    await client.invalidateQueries({ queryKey: meQueryKey })

    await waitFor(() =>
      expect(currentPath(router)).toBe('/sign-in?next=%2Fsettings%2Faccount&reason=expired'),
    )
    expect(
      await screen.findByText('Your session has expired. Sign in again to continue.'),
    ).toBeInTheDocument()
  })
})
