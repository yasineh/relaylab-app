import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'

import { mockApi, signedOut } from '@/test/api'
import { renderApp } from '@/test/render'

describe('app shell', () => {
  it('renders the RelayLab shell with a skip link and landmarks', async () => {
    mockApi(signedOut)
    renderApp('/')
    expect(screen.getByRole('heading', { level: 1, name: 'RelayLab' })).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Skip to content' })).toHaveAttribute('href', '#main')
    expect(screen.getByRole('banner')).toBeInTheDocument()
    expect(screen.getByRole('main')).toBeInTheDocument()
    expect(await screen.findByText('API and database are ready.')).toBeInTheDocument()
  })

  it('shows not-ready checks from a 503 readiness response', async () => {
    mockApi({
      ...signedOut,
      'GET /readyz': {
        status: 503,
        body: { status: 'not_ready', checks: { database: 'ok', migrations: 'fail' } },
      },
    })
    renderApp('/')
    expect(await screen.findByText('The API is not ready.')).toBeInTheDocument()
    expect(screen.getByText(/Migrations: fail/)).toBeInTheDocument()
  })

  it('shows an error state when the API is unreachable and can refresh', async () => {
    const { spy } = mockApi(signedOut)
    const real = spy.getMockImplementation()!
    spy.mockImplementation(async (input, init) => {
      if (String(input).endsWith('/readyz')) throw new TypeError('Failed to fetch')
      return real(input, init)
    })
    renderApp('/')
    expect(await screen.findByText('The API is unreachable.')).toBeInTheDocument()

    spy.mockImplementation(real)
    await userEvent.click(screen.getByRole('button', { name: 'Refresh' }))
    expect(await screen.findByText('API and database are ready.')).toBeInTheDocument()
  })

  it('renders a not-found page with a working link home', async () => {
    mockApi(signedOut)
    renderApp('/does-not-exist')
    expect(screen.getByRole('heading', { name: 'Page not found' })).toBeInTheDocument()
    await userEvent.click(screen.getByRole('link', { name: 'Back to home' }))
    expect(await screen.findByRole('heading', { level: 1, name: 'RelayLab' })).toBeInTheDocument()
  })

  it('offers sign-in and sign-up when signed out', async () => {
    mockApi(signedOut)
    renderApp('/')
    expect(await screen.findByRole('link', { name: 'Sign in' })).toHaveAttribute('href', '/sign-in')
    expect(await screen.findByRole('link', { name: 'Create account' })).toHaveAttribute('href', '/sign-up')
  })
})

vi.setConfig({ testTimeout: 10_000 })
