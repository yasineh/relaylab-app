import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'

import { PID } from '@/features/events/fixtures'
import { EVENT_ID, withReplay } from '@/features/replay/fixtures'
import { me, mockApi, signedIn, signedOut } from '@/test/api'
import { currentPath, renderApp } from '@/test/render'

const demoMe = { ...me, name: 'Demo visitor', is_demo: true, demo_expires_at: '2026-11-14T10:00:00Z' }
const configWithDemo = (enabled: boolean) => ({
  'GET /api/v1/config': { body: { ...signedOut['GET /api/v1/config'].body, demo_enabled: enabled } },
})

describe('try the demo (4.1)', () => {
  it('is offered to signed-out visitors when demos are enabled', async () => {
    mockApi({ ...signedOut, ...configWithDemo(true) })
    renderApp('/')
    expect(await screen.findByRole('button', { name: 'Try the demo' })).toBeInTheDocument()
  })

  it('is not offered when demos are off, or to signed-in users', async () => {
    mockApi({ ...signedOut, ...configWithDemo(false) })
    const first = renderApp('/')
    expect(await screen.findByRole('heading', { name: 'RelayLab', level: 1 })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Try the demo' })).not.toBeInTheDocument()
    first.unmount()

    mockApi({ ...signedIn, ...configWithDemo(true) })
    renderApp('/')
    expect(await screen.findByRole('heading', { name: 'RelayLab', level: 1 })).toBeInTheDocument()
    await waitFor(() =>
      expect(screen.queryByRole('button', { name: 'Try the demo' })).not.toBeInTheDocument(),
    )
  })

  it('signs in anonymously and opens the seeded demo project', async () => {
    let signedInNow = false
    const { calls } = mockApi({
      ...signedOut,
      ...configWithDemo(true),
      'POST /api/auth/sign-in/anonymous': () => {
        signedInNow = true
        return { body: { token: 't', user: { id: 'd1' } } }
      },
      'GET /api/v1/me': () => (signedInNow ? { body: demoMe } : signedOut['GET /api/v1/me']),
      'GET /api/v1/projects': { body: { items: [{ id: PID, name: 'Demo project' }] } },
    })
    const { router } = renderApp('/')
    await userEvent.click(await screen.findByRole('button', { name: 'Try the demo' }))
    await waitFor(() => expect(currentPath(router)).toBe(`/projects/${PID}/events`))
    expect(calls.some((c) => c.method === 'POST' && c.path === '/api/auth/sign-in/anonymous')).toBe(true)
  })

  it('explains the per-network demo limit', async () => {
    mockApi({
      ...signedOut,
      ...configWithDemo(true),
      'POST /api/auth/sign-in/anonymous': {
        status: 429,
        body: { code: 'TOO_MANY_REQUESTS', message: 'Too many requests' },
      },
    })
    renderApp('/')
    await userEvent.click(await screen.findByRole('button', { name: 'Try the demo' }))
    expect(await screen.findByRole('alert')).toHaveTextContent(
      'Too many demos were started from your network',
    )
  })
})

describe('demo banner (4.5)', () => {
  it('labels every dashboard screen of a demo workspace with its expiry and limits', async () => {
    mockApi({ ...signedIn, 'GET /api/v1/me': { body: demoMe } })
    renderApp('/projects')
    const banner = await screen.findByRole('region', { name: 'Demo workspace' })
    expect(banner).toHaveTextContent('synthetic')
    expect(banner).toHaveTextContent('Replay goes to the test sink only')
    expect(within(banner).getByText((_, el) => el?.tagName === 'TIME')).toHaveAttribute(
      'datetime',
      demoMe.demo_expires_at,
    )
  })

  it('is absent for real users', async () => {
    mockApi(signedIn)
    renderApp('/projects')
    expect(await screen.findByRole('heading', { name: 'Create your first project' })).toBeInTheDocument()
    expect(screen.queryByRole('region', { name: 'Demo workspace' })).not.toBeInTheDocument()
  })
})

describe('demo replay (4.4)', () => {
  it('offers the test sink only, with an explanation', async () => {
    withReplay({ 'GET /api/v1/me': { body: demoMe } })
    renderApp(`/projects/${PID}/events/${EVENT_ID}`)
    await userEvent.click(await screen.findByRole('button', { name: 'Replay' }))
    const dialog = await screen.findByRole('dialog', { name: 'Replay event' })
    expect(within(dialog).getByRole('radio', { name: 'URL' })).toBeDisabled()
    expect(within(dialog).getByRole('radio', { name: 'Test sink' })).toBeChecked()
    expect(within(dialog).getByText(/Demo workspaces replay to their test sink only/)).toBeInTheDocument()
  })
})
