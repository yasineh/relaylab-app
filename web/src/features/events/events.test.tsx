import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'

import { EPID, EVENTS, PID, summary, withProject } from '@/features/events/fixtures'
import { apiError, type MockReply } from '@/test/api'
import { currentPath, renderApp } from '@/test/render'

function deferred(reply: MockReply) {
  let release!: () => void
  const gate = new Promise<void>((r) => (release = r))
  return { handler: async () => (await gate, reply), release: () => release() }
}

const table = () => screen.getByRole('table', { name: 'Received events' })

describe('events list (4.1)', () => {
  it('shows a loading state, then events with method, path, endpoint, size, and time', async () => {
    const list = deferred({
      body: {
        items: [summary(), summary({ id: 'e2', method: 'GET', path: '', body_size: 0 })],
        next_cursor: null,
      },
    })
    withProject({ [EVENTS]: list.handler })
    renderApp(`/projects/${PID}/events`)
    expect(await screen.findByText('Loading events…')).toBeInTheDocument()
    list.release()
    const rows = within(await screen.findByRole('table', { name: 'Received events' }))
      .getAllByRole('row')
      .slice(1)
    expect(rows).toHaveLength(2)
    expect(rows[0]).toHaveTextContent('POST')
    expect(rows[0]).toHaveTextContent('/charge/succeeded')
    expect(rows[0]).toHaveTextContent('application/json')
    expect(rows[0]).toHaveTextContent('Stripe staging')
    expect(rows[0]).toHaveTextContent('1.5 KiB')
    expect(within(rows[0]!).getByRole('link', { name: '/charge/succeeded' })).toHaveAttribute(
      'href',
      `/projects/${PID}/events/${summary().id}`,
    )
    expect(within(rows[0]!).getByText((_, el) => el?.tagName === 'TIME')).toHaveAttribute(
      'datetime',
      '2026-11-03T09:15:00Z',
    )
    expect(rows[1]).toHaveTextContent('/') // empty path shown as "/"
    expect(rows[1]).toHaveTextContent('0 bytes')
    expect(screen.getByText('No more events.')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Events' })).toHaveAttribute('aria-current', 'page')
  })

  it('loads more pages with the cursor', async () => {
    const { calls } = withProject({
      [EVENTS]: ({ url }) =>
        url.searchParams.get('cursor') === 'c1'
          ? { body: { items: [summary({ id: 'e3', path: '/third' })], next_cursor: null } }
          : { body: { items: [summary(), summary({ id: 'e2', path: '/second' })], next_cursor: 'c1' } },
    })
    renderApp(`/projects/${PID}/events`)
    await userEvent.click(await screen.findByRole('button', { name: 'Load more' }))
    expect(await screen.findByText('/third')).toBeInTheDocument()
    expect(within(table()).getAllByRole('row')).toHaveLength(4)
    expect(screen.queryByRole('button', { name: 'Load more' })).not.toBeInTheDocument()
    expect(calls.filter((c) => c.path.endsWith('/events'))).toHaveLength(2)
  })

  it('keeps loaded rows when loading more fails', async () => {
    let n = 0
    withProject({
      [EVENTS]: () =>
        n++ === 0 ? { body: { items: [summary()], next_cursor: 'c1' } } : apiError(500, 'internal_error'),
    })
    renderApp(`/projects/${PID}/events`)
    await userEvent.click(await screen.findByRole('button', { name: 'Load more' }))
    expect(await screen.findByText('More events could not be loaded.')).toBeInTheDocument()
    expect(within(table()).getAllByRole('row')).toHaveLength(2)
  })

  it('shows setup instructions with a curl example when the project has no events', async () => {
    withProject({ [EVENTS]: { body: { items: [], next_cursor: null } } })
    renderApp(`/projects/${PID}/events`)
    expect(await screen.findByRole('heading', { name: 'No events yet' })).toBeInTheDocument()
    expect(screen.getByText(/curl -X POST 'https:\/\/relaylab\.test\/h\//)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Copy curl command' })).toBeInTheDocument()
  })

  it('points to endpoints when there is nothing to send to', async () => {
    withProject({
      [EVENTS]: { body: { items: [], next_cursor: null } },
      [`GET /api/v1/projects/${PID}/endpoints`]: { body: { items: [] } },
    })
    renderApp(`/projects/${PID}/events`)
    expect(await screen.findByRole('link', { name: 'Go to endpoints' })).toHaveAttribute(
      'href',
      `/projects/${PID}/endpoints`,
    )
  })

  it('applies filters through the URL and the API, and clears them', async () => {
    let lastQuery = ''
    withProject({
      [EVENTS]: ({ url }) => {
        lastQuery = url.search
        return url.searchParams.get('method')
          ? { body: { items: [], next_cursor: null } }
          : { body: { items: [summary()], next_cursor: null } }
      },
    })
    const { router } = renderApp(`/projects/${PID}/events`)
    await screen.findByRole('table', { name: 'Received events' })
    const form = screen.getByRole('form', { name: 'Filter events' })
    await userEvent.selectOptions(within(form).getByLabelText('Endpoint'), EPID)
    await userEvent.click(within(form).getByRole('checkbox', { name: 'PUT' }))
    await userEvent.click(within(form).getByRole('checkbox', { name: 'POST' }))
    await userEvent.type(within(form).getByLabelText('Content type starts with'), 'application/')
    await userEvent.selectOptions(within(form).getByLabelText('Order'), 'asc')
    await userEvent.click(within(form).getByRole('button', { name: 'Apply filters' }))

    expect(await screen.findByRole('heading', { name: 'No events match these filters' })).toBeInTheDocument()
    expect(currentPath(router)).toBe(
      `/projects/${PID}/events?endpoint_id=${EPID}&method=PUT%2CPOST&content_type=application%2F&sort=asc`,
    )
    expect(lastQuery).toBe(`?endpoint_id=${EPID}&method=PUT%2CPOST&content_type=application%2F&sort=asc`)

    await userEvent.click(screen.getAllByRole('button', { name: 'Clear filters' }).at(-1)!)
    await waitFor(() => expect(currentPath(router)).toBe(`/projects/${PID}/events`))
    expect(await screen.findByRole('table', { name: 'Received events' })).toBeInTheDocument()
    // The form shows the cleared state.
    expect(
      within(screen.getByRole('form', { name: 'Filter events' })).getByRole('checkbox', { name: 'PUT' }),
    ).not.toBeChecked()
  })

  it('restores filters from the URL into the form and the request', async () => {
    let seen: URLSearchParams | undefined
    withProject({
      [EVENTS]: ({ url }) => {
        seen = url.searchParams
        return { body: { items: [summary()], next_cursor: null } }
      },
    })
    renderApp(
      `/projects/${PID}/events?method=get,post&received_after=2026-11-01T12:00:00.000Z&sort=asc&bogus=1`,
    )
    await screen.findByRole('table', { name: 'Received events' })
    expect(seen?.get('method')).toBe('GET,POST')
    expect(seen?.get('received_after')).toBe('2026-11-01T12:00:00.000Z')
    expect(seen?.get('sort')).toBe('asc')
    expect(seen?.has('bogus')).toBe(false)
    const form = screen.getByRole('form', { name: 'Filter events' })
    expect(within(form).getByRole('checkbox', { name: 'GET' })).toBeChecked()
    expect(within(form).getByLabelText('Order')).toHaveValue('asc')
    expect(within(form).getByLabelText('Received after')).not.toHaveValue('')
  })

  it('names invalid filters from a 422', async () => {
    withProject({
      [EVENTS]: {
        status: 422,
        body: {
          error: {
            code: 'validation_failed',
            message: 'Some fields are invalid.',
            request_id: 'r',
            fields: { received_before: 'Must be later than received_after.' },
          },
        },
      },
    })
    renderApp(
      `/projects/${PID}/events?received_after=2026-11-02T00:00:00Z&received_before=2026-11-01T00:00:00Z`,
    )
    expect(await screen.findByText('Must be later than received_after.')).toBeInTheDocument()
    expect(screen.getByLabelText('Received before')).toHaveAttribute('aria-invalid', 'true')
    expect(screen.getByRole('alert')).toHaveTextContent('Some filters are invalid')
  })

  it('shows an error with retry', async () => {
    let fail = true
    withProject({
      [EVENTS]: () =>
        fail ? apiError(500, 'internal_error') : { body: { items: [summary()], next_cursor: null } },
    })
    renderApp(`/projects/${PID}/events`)
    expect(await screen.findByText('The events could not be loaded.')).toBeInTheDocument()
    fail = false
    await userEvent.click(screen.getByRole('button', { name: 'Try again' }))
    expect(await screen.findByRole('table', { name: 'Received events' })).toBeInTheDocument()
  })
})
