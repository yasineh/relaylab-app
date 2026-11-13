import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'

import { mockApi, signedOut } from '@/test/api'
import { renderApp } from '@/test/render'

const LANDING_H1 = 'Catch a webhook, read every byte, send it again.'

describe('app shell', () => {
  it('renders the shell with a skip link, header, main, and footer', async () => {
    mockApi(signedOut)
    renderApp('/')
    expect(await screen.findByRole('heading', { level: 1, name: LANDING_H1 })).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Skip to content' })).toHaveAttribute('href', '#main')
    expect(screen.getByRole('banner')).toBeInTheDocument()
    expect(screen.getByRole('main')).toBeInTheDocument()
    expect(screen.getByRole('contentinfo')).toBeInTheDocument()
  })

  it('renders a not-found page with a working link home', async () => {
    mockApi(signedOut)
    renderApp('/does-not-exist')
    expect(screen.getByRole('heading', { name: 'Page not found' })).toBeInTheDocument()
    await userEvent.click(screen.getByRole('link', { name: 'Back to home' }))
    expect(await screen.findByRole('heading', { level: 1, name: LANDING_H1 })).toBeInTheDocument()
  })

  it('offers sign-in and sign-up in the header when signed out', async () => {
    mockApi(signedOut)
    renderApp('/')
    const header = screen.getByRole('banner')
    expect(await screen.findByRole('navigation', { name: 'Account' })).toBeInTheDocument()
    const links = Array.from(header.querySelectorAll('a')).map((a) => [a.textContent, a.getAttribute('href')])
    expect(links).toContainEqual(['Sign in', '/sign-in'])
    expect(links).toContainEqual(['Create account', '/sign-up'])
  })
})

vi.setConfig({ testTimeout: 10_000 })
