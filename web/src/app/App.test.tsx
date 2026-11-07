import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { createMemoryRouter } from 'react-router'
import { describe, expect, it, vi } from 'vitest'

import { App, createQueryClient } from '@/app/App'
import { routes } from '@/app/routes'

function renderAt(path: string) {
  const router = createMemoryRouter(routes, { initialEntries: [path] })
  const queryClient = createQueryClient()
  queryClient.setDefaultOptions({ queries: { retry: false } })
  return render(<App router={router} queryClient={queryClient} />)
}

function mockReadyz(status: number, body: unknown) {
  return vi.spyOn(globalThis, 'fetch').mockResolvedValue(
    new Response(JSON.stringify(body), {
      status,
      headers: { 'Content-Type': 'application/json' },
    }),
  )
}

describe('app shell', () => {
  it('renders the RelayLab shell with a skip link and landmarks', async () => {
    mockReadyz(200, { status: 'ready' })
    renderAt('/')
    expect(screen.getByRole('heading', { level: 1, name: 'RelayLab' })).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Skip to content' })).toHaveAttribute('href', '#main')
    expect(screen.getByRole('banner')).toBeInTheDocument()
    expect(screen.getByRole('main')).toBeInTheDocument()
    expect(await screen.findByText('API and database are ready.')).toBeInTheDocument()
  })

  it('shows not-ready checks from a 503 readiness response', async () => {
    mockReadyz(503, { status: 'not_ready', checks: { database: 'ok', migrations: 'fail' } })
    renderAt('/')
    expect(await screen.findByText('The API is not ready.')).toBeInTheDocument()
    expect(screen.getByText(/Migrations: fail/)).toBeInTheDocument()
  })

  it('shows an error state when the API is unreachable and can refresh', async () => {
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockRejectedValue(new TypeError('Failed to fetch'))
    renderAt('/')
    expect(await screen.findByText('The API is unreachable.')).toBeInTheDocument()

    fetchMock.mockResolvedValue(new Response(JSON.stringify({ status: 'ready' }), { status: 200 }))
    await userEvent.click(screen.getByRole('button', { name: 'Refresh' }))
    expect(await screen.findByText('API and database are ready.')).toBeInTheDocument()
  })

  it('renders a not-found page with a working link home', async () => {
    mockReadyz(200, { status: 'ready' })
    renderAt('/does-not-exist')
    expect(screen.getByRole('heading', { name: 'Page not found' })).toBeInTheDocument()
    await userEvent.click(screen.getByRole('link', { name: 'Back to home' }))
    expect(await screen.findByRole('heading', { level: 1, name: 'RelayLab' })).toBeInTheDocument()
  })
})
