import { screen, waitFor, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'

import { routes } from '@/app/routes'
import { me, mockApi, signedIn, signedOut } from '@/test/api'
import { axeViolations } from '@/test/axe'
import { renderApp } from '@/test/render'

type Config = Record<string, unknown>
const withConfig = (base: typeof signedOut | typeof signedIn, cfg: Config) =>
  mockApi({ ...base, 'GET /api/v1/config': { body: { ...signedOut['GET /api/v1/config'].body, ...cfg } } })

const hero = () => screen.getByRole('region', { name: /Catch a webhook/ })

describe('landing page structure (1.2, 1.3)', () => {
  it('has one h1, the landmarks, the three workflow steps, and the feature list', async () => {
    withConfig(signedOut, { retention: { event_ttl_seconds: 86_400, max_events_per_project: 200 } })
    renderApp('/')
    expect(await screen.findAllByRole('heading', { level: 1 })).toHaveLength(1)
    for (const role of ['banner', 'main', 'contentinfo'] as const)
      expect(screen.getByRole(role)).toBeInTheDocument()
    expect(screen.getByRole('navigation', { name: 'Footer' })).toBeInTheDocument()

    const steps = within(screen.getByRole('region', { name: 'How it works' })).getAllByRole('listitem')
    expect(steps.map((s) => within(s).getByRole('heading').textContent)).toEqual([
      'Receive',
      'Inspect',
      'Replay',
    ])
    for (const s of steps) expect(within(s).getByRole('img').getAttribute('alt')?.length).toBeGreaterThan(20)

    const features = within(screen.getByRole('region', { name: 'What you get' }))
    expect(features.getAllByRole('term').length).toBeGreaterThanOrEqual(6)
    expect(await features.findByText(/Requests are deleted after 24 hours/)).toBeInTheDocument()
  })

  it('shows the received request with its credentials removed', async () => {
    withConfig(signedOut, {})
    renderApp('/')
    const panel = await screen.findByRole('figure')
    expect(panel).toHaveTextContent('POST /orders?store=demo&token=[REDACTED]')
    expect(panel).toHaveTextContent('Authorization: removed at receipt')
    expect(panel.textContent).not.toMatch(/Bearer|demo_bearer/)
  })
})

describe('calls to action (2.1)', () => {
  it('signed out, demo and sign-up on: Try the demo, Create account, and Sign in', async () => {
    withConfig(signedOut, { demo_enabled: true, signup_enabled: true })
    renderApp('/')
    expect(await within(hero()).findByRole('button', { name: 'Try the demo' })).toBeInTheDocument()
    expect(within(hero()).getByRole('link', { name: 'Create account' })).toHaveAttribute('href', '/sign-up')
    expect(within(hero()).getByRole('link', { name: 'Sign in' })).toHaveAttribute('href', '/sign-in')
  })

  it('demo off and sign-up off (the production default): only Sign in', async () => {
    withConfig(signedOut, { demo_enabled: false, signup_enabled: false })
    renderApp('/')
    expect(await within(hero()).findByRole('link', { name: 'Sign in' })).toBeInTheDocument()
    expect(within(hero()).queryByRole('button', { name: 'Try the demo' })).not.toBeInTheDocument()
    expect(within(hero()).queryByRole('link', { name: 'Create account' })).not.toBeInTheDocument()
  })

  it('signed in: Open dashboard instead of the sign-up and demo actions', async () => {
    mockApi({
      ...signedIn,
      'GET /api/v1/config': { body: { ...signedOut['GET /api/v1/config'].body, demo_enabled: true } },
      'GET /api/v1/me': { body: me },
    })
    renderApp('/')
    expect(await within(hero()).findByRole('link', { name: 'Open dashboard' })).toHaveAttribute(
      'href',
      '/projects',
    )
    await waitFor(() =>
      expect(within(hero()).queryByRole('button', { name: 'Try the demo' })).not.toBeInTheDocument(),
    )
    expect(within(hero()).queryByRole('link', { name: 'Sign in' })).not.toBeInTheDocument()
  })
})

describe('demo video and footer links (2.2, 2.3)', () => {
  it('links the video only when configured, in a new tab without opener', async () => {
    withConfig(signedOut, {})
    const first = renderApp('/')
    await within(hero()).findByRole('link', { name: 'Sign in' })
    expect(screen.queryByRole('link', { name: /Watch the demo/ })).not.toBeInTheDocument()
    expect(document.body.textContent).not.toMatch(/coming soon/i)
    first.unmount()

    withConfig(signedOut, { demo_video_url: 'https://youtu.be/demo123' })
    renderApp('/')
    const video = await screen.findByRole('link', { name: /Watch the demo/ })
    expect(video).toHaveAttribute('href', 'https://youtu.be/demo123')
    expect(video).toHaveAttribute('target', '_blank')
    expect(video).toHaveAttribute('rel', 'noopener noreferrer')
  })

  it('shows repository and documentation links only when a public repository is configured', async () => {
    withConfig(signedOut, {})
    const first = renderApp('/')
    const footer = await screen.findByRole('navigation', { name: 'Footer' })
    expect(within(footer).getByRole('link', { name: 'Security and privacy' })).toHaveAttribute(
      'href',
      '/security',
    )
    expect(within(footer).queryByRole('link', { name: 'Source code' })).not.toBeInTheDocument()
    first.unmount()

    withConfig(signedOut, { repo_url: 'https://github.com/example/relaylab' })
    renderApp('/')
    const f = await screen.findByRole('navigation', { name: 'Footer' })
    expect(await within(f).findByRole('link', { name: 'Source code' })).toHaveAttribute(
      'href',
      'https://github.com/example/relaylab',
    )
    expect(within(f).getByRole('link', { name: 'Documentation' })).toHaveAttribute(
      'href',
      'https://github.com/example/relaylab/tree/main/docs',
    )
  })

  it('has no placeholder links: every internal link is a real route', async () => {
    withConfig(signedOut, {
      demo_enabled: true,
      signup_enabled: true,
      demo_video_url: 'https://youtu.be/x',
      repo_url: 'https://github.com/example/relaylab',
    })
    renderApp('/')
    await screen.findByRole('link', { name: /Watch the demo/ })
    const known = new Set(
      routes.flatMap((r) => [
        r.path,
        ...(r.children ?? []).flatMap((c) => [c.path, ...(c.children ?? []).map((g) => g.path)]),
      ]),
    )
    const problems: string[] = []
    for (const a of Array.from(document.querySelectorAll('a'))) {
      const href = a.getAttribute('href') ?? ''
      const label = `${a.textContent}: ${href}`
      if (href.startsWith('#') && href.length > 1) {
        // In-page anchors (the skip link) must point at an element that exists.
        if (!document.getElementById(href.slice(1))) problems.push(`${label} has no target`)
      } else if (/^(#|javascript:|$)/.test(href)) {
        problems.push(`${label} is a placeholder`)
      } else if (href.startsWith('/')) {
        const first = href.split(/[?#]/)[0]!.replace(/^\//, '')
        if (first !== '' && !known.has(first)) problems.push(`${label} is not a route`)
      } else if (!href.startsWith('https://')) {
        problems.push(`${label} is not https`)
      }
    }
    expect(problems).toEqual([])
  })
})

describe('security page', () => {
  it('summarises storage, credential removal, retention, and replay limits', async () => {
    withConfig(signedOut, { retention: { event_ttl_seconds: 86_400, max_events_per_project: 200 } })
    renderApp('/security')
    expect(await screen.findByRole('heading', { level: 1, name: 'Security and privacy' })).toBeInTheDocument()
    for (const h of [
      'What is stored',
      'Credentials are removed on arrival',
      'How long data is kept',
      'What replay can reach',
    ]) {
      expect(screen.getByRole('heading', { name: h })).toBeInTheDocument()
    }
    expect(await screen.findByText(/deleted after 24 hours/)).toBeInTheDocument()
  })
})

describe('accessibility (3.2)', () => {
  it('has no axe violations', async () => {
    withConfig(signedOut, { demo_enabled: true, signup_enabled: true, demo_video_url: 'https://youtu.be/x' })
    renderApp('/')
    await screen.findByRole('button', { name: 'Try the demo' })
    expect(await axeViolations()).toEqual([])
  })
})
