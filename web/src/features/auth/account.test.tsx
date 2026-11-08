import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'

import { authError, me, mockApi, signedIn, type MockReply } from '@/test/api'
import { currentPath, renderApp } from '@/test/render'

const CURRENT = 'violet-anchor-89-orbit'
const NEXT = 'harbor-lantern-42-quartz'

/** A reply that waits until `release()` is called, to observe loading states. */
function deferred(reply: MockReply) {
  let release!: () => void
  const gate = new Promise<void>((r) => (release = r))
  return { handler: async () => (await gate, reply), release: () => release() }
}

const section = (name: string) => screen.getByRole('region', { name })

describe('account settings', () => {
  it('shows the signed-in email', async () => {
    mockApi(signedIn)
    renderApp('/settings/account')
    expect(await screen.findByText('ada@example.test')).toBeInTheDocument()
  })

  describe('display name', () => {
    it('saves with a loading state, then confirms and refreshes the user', async () => {
      let name = me.name
      const save = deferred({ body: { status: true } })
      const { calls } = mockApi({
        ...signedIn,
        'GET /api/v1/me': () => ({ body: { ...me, name } }),
        'POST /api/auth/update-user': async (req) => {
          const reply = await save.handler()
          name = (req.body as { name: string }).name
          return reply
        },
      })
      renderApp('/settings/account')
      const profile = await waitFor(() => section('Profile'))
      const input = await within(profile).findByDisplayValue('Ada Lovelace')
      await userEvent.clear(input)
      await userEvent.type(input, '  Ada King  ')
      await userEvent.click(within(profile).getByRole('button', { name: 'Save name' }))

      expect(within(profile).getByRole('button', { name: 'Saving…' })).toBeDisabled()
      save.release()
      expect(await within(profile).findByText('Your display name was updated.')).toBeInTheDocument()
      expect(calls.find((c) => c.path === '/api/auth/update-user')?.body).toEqual({ name: 'Ada King' })
      expect(await screen.findByRole('link', { name: /Ada King/ })).toBeInTheDocument()
    })

    it('validates and shows server errors', async () => {
      mockApi({ ...signedIn, 'POST /api/auth/update-user': authError(400, 'INVALID_NAME') })
      renderApp('/settings/account')
      const profile = await waitFor(() => section('Profile'))
      const input = await within(profile).findByDisplayValue('Ada Lovelace')
      await userEvent.clear(input)
      await userEvent.click(within(profile).getByRole('button', { name: 'Save name' }))
      expect(input).toHaveAccessibleDescription('Enter your name.')

      await userEvent.type(input, 'Ada')
      await userEvent.click(within(profile).getByRole('button', { name: 'Save name' }))
      await waitFor(() => expect(input).toHaveAccessibleDescription('Enter a name of 1–80 characters.'))
    })
  })

  describe('change password', () => {
    it('changes the password, revokes other sessions, and confirms', async () => {
      const change = deferred({ body: { token: null } })
      const { calls } = mockApi({ ...signedIn, 'POST /api/auth/change-password': change.handler })
      renderApp('/settings/account')
      const pw = await waitFor(() => section('Password'))
      await userEvent.type(within(pw).getByLabelText('Current password'), CURRENT)
      await userEvent.type(within(pw).getByLabelText('New password'), NEXT)
      await userEvent.type(within(pw).getByLabelText('Confirm new password'), NEXT)
      await userEvent.click(within(pw).getByRole('button', { name: 'Change password' }))

      expect(within(pw).getByRole('button', { name: 'Changing…' })).toBeDisabled()
      change.release()
      expect(await within(pw).findByText(/signed out on every other device/)).toBeInTheDocument()
      expect(calls.find((c) => c.path === '/api/auth/change-password')?.body).toEqual({
        currentPassword: CURRENT,
        newPassword: NEXT,
        revokeOtherSessions: true,
      })
      expect(within(pw).getByLabelText('Current password')).toHaveValue('')
    })

    it('shows a wrong current password on its field', async () => {
      mockApi({ ...signedIn, 'POST /api/auth/change-password': authError(400, 'INVALID_PASSWORD') })
      renderApp('/settings/account')
      const pw = await waitFor(() => section('Password'))
      await userEvent.type(within(pw).getByLabelText('Current password'), 'wrong-password')
      await userEvent.type(within(pw).getByLabelText('New password'), NEXT)
      await userEvent.type(within(pw).getByLabelText('Confirm new password'), NEXT)
      await userEvent.click(within(pw).getByRole('button', { name: 'Change password' }))
      await waitFor(() =>
        expect(within(pw).getByLabelText('Current password')).toHaveAccessibleDescription(
          'The current password is incorrect.',
        ),
      )
    })

    it('validates length and confirmation before calling the server', async () => {
      const { calls } = mockApi(signedIn)
      renderApp('/settings/account')
      const pw = await waitFor(() => section('Password'))
      await userEvent.type(within(pw).getByLabelText('Current password'), CURRENT)
      await userEvent.type(within(pw).getByLabelText('New password'), 'short')
      await userEvent.type(within(pw).getByLabelText('Confirm new password'), 'other')
      await userEvent.click(within(pw).getByRole('button', { name: 'Change password' }))
      expect(within(pw).getByLabelText('New password')).toHaveAccessibleDescription(
        'Use at least 12 characters.',
      )
      expect(within(pw).getByLabelText('Confirm new password')).toHaveAccessibleDescription(
        'Passwords do not match.',
      )
      expect(calls.some((c) => c.path === '/api/auth/change-password')).toBe(false)
    })
  })

  describe('sign out everywhere', () => {
    it('revokes all sessions and returns to sign-in', async () => {
      const revoke = deferred({ body: { status: true } })
      const { calls } = mockApi({ ...signedIn, 'POST /api/auth/revoke-sessions': revoke.handler })
      const { router } = renderApp('/settings/account')
      const sessions = await waitFor(() => section('Sessions'))
      await userEvent.click(within(sessions).getByRole('button', { name: 'Sign out everywhere' }))
      expect(within(sessions).getByRole('button', { name: 'Signing out…' })).toBeDisabled()
      revoke.release()
      await waitFor(() => expect(currentPath(router)).toBe('/sign-in?reason=signed-out'))
      expect(calls.some((c) => c.path === '/api/auth/revoke-sessions')).toBe(true)
    })

    it('shows an error and stays when revocation fails', async () => {
      mockApi({ ...signedIn, 'POST /api/auth/revoke-sessions': authError(500, '') })
      const { router } = renderApp('/settings/account')
      const sessions = await waitFor(() => section('Sessions'))
      await userEvent.click(within(sessions).getByRole('button', { name: 'Sign out everywhere' }))
      expect(await within(sessions).findByRole('alert')).toHaveTextContent('Something went wrong')
      expect(currentPath(router)).toBe('/settings/account')
    })
  })

  describe('delete account', () => {
    it('requires opening the form, the password, and an explicit confirmation', async () => {
      const del = deferred({ body: { success: true } })
      const { calls } = mockApi({ ...signedIn, 'POST /api/auth/delete-user': del.handler })
      const { router } = renderApp('/settings/account')
      const danger = await waitFor(() => section('Delete account'))
      expect(within(danger).queryByLabelText('Password')).not.toBeInTheDocument()
      expect(danger).toHaveTextContent('including all projects and their events')

      await userEvent.click(within(danger).getByRole('button', { name: 'Delete account…' }))
      await userEvent.click(within(danger).getByRole('button', { name: 'Permanently delete account' }))
      expect(within(danger).getByLabelText('Password')).toHaveAccessibleDescription(
        'Enter your password to confirm.',
      )
      expect(within(danger).getByRole('checkbox')).toHaveAccessibleDescription(
        'Confirm that you understand this cannot be undone.',
      )
      expect(calls.some((c) => c.path === '/api/auth/delete-user')).toBe(false)

      await userEvent.type(within(danger).getByLabelText('Password'), CURRENT)
      await userEvent.click(within(danger).getByRole('checkbox'))
      await userEvent.click(within(danger).getByRole('button', { name: 'Permanently delete account' }))
      expect(within(danger).getByRole('button', { name: 'Deleting…' })).toBeDisabled()
      del.release()
      await waitFor(() => expect(currentPath(router)).toBe('/sign-in?reason=account-deleted'))
      expect(calls.find((c) => c.path === '/api/auth/delete-user')?.body).toEqual({ password: CURRENT })
    })

    it('shows a wrong password and can be cancelled', async () => {
      mockApi({ ...signedIn, 'POST /api/auth/delete-user': authError(400, 'INVALID_PASSWORD') })
      renderApp('/settings/account')
      const danger = await waitFor(() => section('Delete account'))
      await userEvent.click(within(danger).getByRole('button', { name: 'Delete account…' }))
      await userEvent.type(within(danger).getByLabelText('Password'), 'wrong-password')
      await userEvent.click(within(danger).getByRole('checkbox'))
      await userEvent.click(within(danger).getByRole('button', { name: 'Permanently delete account' }))
      await waitFor(() =>
        expect(within(danger).getByLabelText('Password')).toHaveAccessibleDescription(
          'The password is incorrect.',
        ),
      )
      await userEvent.click(within(danger).getByRole('button', { name: 'Cancel' }))
      expect(within(danger).queryByLabelText('Password')).not.toBeInTheDocument()
    })
  })
})
