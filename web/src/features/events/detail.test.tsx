import { screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'

import type { EventDetail } from '@/features/events/api'
import { PID, summary, withProject } from '@/features/events/fixtures'
import { apiError } from '@/test/api'
import { renderApp } from '@/test/render'

const ID = summary().id
const ONE = `GET /api/v1/projects/${PID}/events/${ID}`

const detail = (over: Partial<EventDetail> = {}): EventDetail => ({
  ...summary(),
  raw_query: 'token=%5BREDACTED%5D&page=2',
  query: [
    { name: 'page', value: '2', redacted: false },
    { name: 'token', value: '[REDACTED]', redacted: true },
  ],
  headers: [
    { name: 'Authorization', value: '[REDACTED]', redacted: true },
    { name: 'Content-Type', value: 'application/json', redacted: false },
    { name: 'X-Note', value: '[REDACTED]', redacted: false },
  ],
  redacted_count: 2,
  body: {
    encoding: 'utf8',
    text: '{"id":"evt_1","amount":1200,"nested":{"a":{"b":{"c":1}}}}',
    is_json: true,
    json_invalid: false,
    size: 58,
  },
  source_ip_prefix: '203.0.113.0/24',
  user_agent: 'Stripe/1.0',
  correlation_id: 'req-123',
  ...over,
})

const open = (tab: string) => userEvent.click(screen.getByRole('tab', { name: new RegExp(`^${tab}`) }))

describe('event detail (4.2)', () => {
  it('shows the summary, overview, and confidentiality banner', async () => {
    withProject({ [ONE]: { body: detail() } })
    renderApp(`/projects/${PID}/events/${ID}`)
    expect(
      await screen.findByRole('heading', { name: '/charge/succeeded?token=%5BREDACTED%5D&page=2' }),
    ).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Stripe staging' })).toHaveAttribute(
      'href',
      `/projects/${PID}/endpoints/${summary().endpoint_id}`,
    )
    expect(screen.getByRole('status')).toHaveTextContent(/confidential or personal data/)
    expect(screen.getByRole('status')).toHaveTextContent(
      '2 credential values were removed when the request arrived.',
    )
    const panel = screen.getByRole('tabpanel')
    expect(within(panel).getByText('203.0.113.0/24')).toBeInTheDocument()
    expect(within(panel).getByText('req-123')).toBeInTheDocument()
    expect(screen.getByRole('tab', { name: 'Overview' })).toHaveAttribute('aria-selected', 'true')
  })

  it('shows the JSON body as a tree, with a raw view and copy', async () => {
    const user = userEvent.setup()
    withProject({ [ONE]: { body: detail() } })
    const { router } = renderApp(`/projects/${PID}/events/${ID}`)
    await screen.findByRole('tablist', { name: 'Event details' })
    await user.click(screen.getByRole('tab', { name: 'Body' }))
    expect(router.state.location.search).toBe('?tab=body')
    expect(screen.getByRole('list', { name: 'JSON body' })).toHaveTextContent('"evt_1"')
    // nested (1) > a (2) are open; b is depth 3, so it starts collapsed and c is hidden.
    expect(screen.getByRole('button', { name: /"b"/ })).toHaveAttribute('aria-expanded', 'false')
    expect(screen.queryByText('"c"')).not.toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Raw' }))
    expect(screen.getByLabelText('Raw body')).toHaveTextContent(detail().body.text!)
    await user.click(screen.getByRole('button', { name: 'Copy body' }))
    expect(await navigator.clipboard.readText()).toBe(detail().body.text)
    expect(screen.getByRole('link', { name: 'Download raw' })).toHaveAttribute(
      'href',
      `/api/v1/projects/${PID}/events/${ID}/body`,
    )
  })

  it('explains invalid JSON and shows it raw', async () => {
    withProject({
      [ONE]: {
        body: detail({
          body: { encoding: 'utf8', text: '{"a":', is_json: false, json_invalid: true, size: 5 },
        }),
      },
    })
    renderApp(`/projects/${PID}/events/${ID}?tab=body`)
    expect(await screen.findByText(/not valid JSON/)).toBeInTheDocument()
    expect(screen.getByLabelText('Raw body')).toHaveTextContent('{"a":')
    expect(screen.queryByRole('button', { name: 'Tree' })).not.toBeInTheDocument()
  })

  it('shows binary bodies as hex', async () => {
    withProject({
      [ONE]: {
        body: detail({
          body: { encoding: 'binary', base64: '//4A', is_json: false, json_invalid: false, size: 3 },
        }),
      },
    })
    renderApp(`/projects/${PID}/events/${ID}?tab=body`)
    expect(await screen.findByLabelText('Hex dump')).toHaveTextContent('00000000 ff fe 00')
    expect(screen.getByRole('link', { name: 'Download raw' })).toBeInTheDocument()
  })

  it('marks removed values with text and an explanation, and copies [REDACTED]', async () => {
    const user = userEvent.setup()
    withProject({ [ONE]: { body: detail() } })
    renderApp(`/projects/${PID}/events/${ID}?tab=headers`)
    const table = await screen.findByRole('table', { name: 'Request headers' })
    const auth = within(table).getByRole('row', { name: /Authorization/ })
    expect(within(auth).getByText('Removed at receipt')).toHaveAccessibleDescription(
      /does not store credential values/,
    )
    expect(within(auth).queryByText('[REDACTED]')).not.toBeInTheDocument()
    // A sender's literal "[REDACTED]" on an ordinary header is shown as the value it is.
    expect(
      within(within(table).getByRole('row', { name: /X-Note/ })).getByText('[REDACTED]'),
    ).toBeInTheDocument()
    expect(
      within(screen.getByRole('tabpanel')).getByText(/were discarded when it arrived/),
    ).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: 'Copy headers' }))
    expect(await navigator.clipboard.readText()).toBe(
      'Authorization: [REDACTED]\nContent-Type: application/json\nX-Note: [REDACTED]',
    )
    expect(screen.getByText('Copied text contains [REDACTED] for removed values.')).toBeInTheDocument()

    await user.click(screen.getByRole('tab', { name: /^Query/ }))
    const q = screen.getByRole('table', { name: 'Query parameters' })
    expect(
      within(within(q).getByRole('row', { name: /token/ })).getByText('Removed at receipt'),
    ).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Copy query parameters' }))
    expect(await navigator.clipboard.readText()).toBe('page=2\ntoken=[REDACTED]')
  })

  it('has no reveal control anywhere', async () => {
    withProject({ [ONE]: { body: detail() } })
    renderApp(`/projects/${PID}/events/${ID}`)
    await screen.findByRole('tablist', { name: 'Event details' })
    for (const tab of ['Overview', 'Body', 'Headers', 'Query', 'Raw request']) {
      await open(tab)
      expect(
        screen.queryByRole('button', { name: /reveal|show (value|secret)|unmask/i }),
      ).not.toBeInTheDocument()
      expect(screen.queryByRole('link', { name: /reveal/i })).not.toBeInTheDocument()
    }
  })

  it('shows the raw request', async () => {
    withProject({ [ONE]: { body: detail() } })
    renderApp(`/projects/${PID}/events/${ID}?tab=raw`)
    expect(await screen.findByLabelText('Raw request text')).toHaveTextContent(
      /^POST \/charge\/succeeded\?token=%5BREDACTED%5D&page=2 HTTP\/1\.1\s*Authorization: \[REDACTED\]/,
    )
  })

  it('moves between tabs with the arrow keys', async () => {
    const user = userEvent.setup()
    withProject({ [ONE]: { body: detail() } })
    renderApp(`/projects/${PID}/events/${ID}`)
    const overview = await screen.findByRole('tab', { name: 'Overview' })
    overview.focus()
    await user.keyboard('{ArrowRight}')
    expect(screen.getByRole('tab', { name: 'Body' })).toHaveFocus()
    expect(screen.getByRole('tab', { name: 'Body' })).toHaveAttribute('aria-selected', 'true')
    await user.keyboard('{End}')
    expect(screen.getByRole('tab', { name: 'Raw request' })).toHaveFocus()
    await user.keyboard('{ArrowRight}')
    expect(screen.getByRole('tab', { name: 'Overview' })).toHaveFocus()
    expect(screen.getAllByRole('tab').filter((t) => t.tabIndex === 0)).toHaveLength(1)
  })

  it('shows not found and error states', async () => {
    withProject({ [ONE]: apiError(404, 'not_found') })
    renderApp(`/projects/${PID}/events/${ID}`)
    expect(await screen.findByRole('heading', { name: 'Event not found' })).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Back to events' })).toHaveAttribute(
      'href',
      `/projects/${PID}/events`,
    )
  })

  it('offers a retry on errors', async () => {
    withProject({ [ONE]: apiError(500, 'internal_error') })
    renderApp(`/projects/${PID}/events/${ID}`)
    expect(await screen.findByText('This event could not be loaded.')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Try again' })).toBeInTheDocument()
  })
})
