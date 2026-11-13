import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'

import type { EventDetail } from '@/features/events/api'
import { endpoint, EPID, EVENTS, PID, summary, withProject } from '@/features/events/fixtures'
import { formatRetention } from '@/lib/format'
import { apiError, signedOut } from '@/test/api'
import { currentPath, renderApp } from '@/test/render'

const ID = summary().id
const EVENT = `/api/v1/projects/${PID}/events/${ID}`
const ENDPOINT = `/api/v1/projects/${PID}/endpoints/${EPID}`
const CLEAR = `${ENDPOINT}/events`

const detail = (): EventDetail => ({
  ...summary(),
  raw_query: '',
  query: [],
  headers: [],
  redacted_count: 0,
  body: { encoding: 'utf8', text: '{}', is_json: true, json_invalid: false, size: 2 },
  source_ip_prefix: null,
  user_agent: null,
  correlation_id: 'req-1',
})

const config = (ttl: number) => ({
  'GET /api/v1/config': {
    body: {
      ...signedOut['GET /api/v1/config'].body,
      retention: { event_ttl_seconds: ttl, max_events_per_project: 10000 },
    },
  },
})

describe('delete an event (3.3)', () => {
  it('cancel sends nothing and keeps the event', async () => {
    const { calls } = withProject({ [`GET ${EVENT}`]: { body: detail() } })
    renderApp(`/projects/${PID}/events/${ID}`)
    const trigger = await screen.findByRole('button', { name: 'Delete event' })
    await userEvent.click(trigger)
    const dialog = await screen.findByRole('dialog', { name: 'Delete this event?' })
    expect(dialog).toHaveAccessibleDescription(/cannot be undone/)
    // Focus starts on Cancel, so Enter by accident does not delete.
    expect(within(dialog).getByRole('button', { name: 'Cancel' })).toHaveFocus()
    await userEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }))
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    expect(trigger).toHaveFocus()
    expect(calls.some((c) => c.method === 'DELETE')).toBe(false)
  })

  it('confirm deletes and returns to the event list, which no longer shows it', async () => {
    let deleted = false
    const { calls } = withProject({
      [`GET ${EVENT}`]: { body: detail() },
      [`DELETE ${EVENT}`]: () => ((deleted = true), { status: 204 }),
      [EVENTS]: () => ({ body: { items: deleted ? [] : [summary()], next_cursor: null } }),
    })
    const { router } = renderApp(`/projects/${PID}/events/${ID}`)
    await userEvent.click(await screen.findByRole('button', { name: 'Delete event' }))
    const dialog = await screen.findByRole('dialog')
    await userEvent.click(within(dialog).getByRole('button', { name: 'Delete event' }))
    await waitFor(() => expect(currentPath(router)).toBe(`/projects/${PID}/events`))
    expect(await screen.findByRole('heading', { name: 'No events yet' })).toBeInTheDocument()
    expect(calls.filter((c) => c.method === 'DELETE' && c.path === EVENT)).toHaveLength(1)
  })

  it('a failure keeps the dialog open with an error', async () => {
    withProject({
      [`GET ${EVENT}`]: { body: detail() },
      [`DELETE ${EVENT}`]: apiError(500, 'internal_error'),
    })
    const { router } = renderApp(`/projects/${PID}/events/${ID}`)
    await userEvent.click(await screen.findByRole('button', { name: 'Delete event' }))
    const dialog = await screen.findByRole('dialog')
    await userEvent.click(within(dialog).getByRole('button', { name: 'Delete event' }))
    expect(await within(dialog).findByRole('alert')).toHaveTextContent('could not be deleted')
    expect(currentPath(router)).toBe(`/projects/${PID}/events/${ID}`)
  })
})

describe('clear an endpoint’s events (3.4)', () => {
  it('keeps the button disabled until the exact name is typed, then repeats until nothing is left', async () => {
    const replies = [120, 0]
    const { calls } = withProject({
      [`GET ${ENDPOINT}`]: { body: endpoint },
      [`DELETE ${CLEAR}`]: () => ({ body: { deleted: replies.shift() } }),
    })
    renderApp(`/projects/${PID}/endpoints/${EPID}`)
    await userEvent.click(await screen.findByRole('button', { name: 'Clear events' }))
    const dialog = await screen.findByRole('dialog', { name: 'Clear all events?' })
    expect(dialog).toHaveAccessibleDescription(/cannot be undone/)
    const typed = within(dialog).getByLabelText(/to confirm/)
    const confirm = within(dialog).getByRole('button', { name: 'Clear events' })
    expect(typed).toHaveFocus()
    expect(confirm).toBeDisabled()
    await userEvent.type(typed, endpoint.name.toLowerCase())
    expect(confirm).toBeDisabled()
    await userEvent.clear(typed)
    await userEvent.type(typed, `${endpoint.name}{Enter}`)

    expect(await screen.findByText('Deleted 120 events.')).toBeInTheDocument()
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    expect(calls.filter((c) => c.method === 'DELETE' && c.path === CLEAR)).toHaveLength(2)
    // The endpoint itself stays.
    expect(calls.some((c) => c.method === 'DELETE' && c.path === ENDPOINT)).toBe(false)
  })

  it('cancel sends nothing', async () => {
    const { calls } = withProject({ [`GET ${ENDPOINT}`]: { body: endpoint } })
    renderApp(`/projects/${PID}/endpoints/${EPID}`)
    await userEvent.click(await screen.findByRole('button', { name: 'Clear events' }))
    const dialog = await screen.findByRole('dialog')
    await userEvent.type(within(dialog).getByLabelText(/to confirm/), endpoint.name)
    await userEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }))
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    expect(calls.some((c) => c.method === 'DELETE')).toBe(false)
  })

  it('shows an error and keeps the dialog open when clearing fails', async () => {
    withProject({
      [`GET ${ENDPOINT}`]: { body: endpoint },
      [`DELETE ${CLEAR}`]: apiError(500, 'internal_error'),
    })
    renderApp(`/projects/${PID}/endpoints/${EPID}`)
    await userEvent.click(await screen.findByRole('button', { name: 'Clear events' }))
    const dialog = await screen.findByRole('dialog')
    await userEvent.type(within(dialog).getByLabelText(/to confirm/), endpoint.name)
    await userEvent.click(within(dialog).getByRole('button', { name: 'Clear events' }))
    expect(await within(dialog).findByRole('alert')).toHaveTextContent('could not all be deleted')
  })
})

describe('retention transparency (4.2)', () => {
  it.each([
    [86_400, '24 hours'],
    [3_600, '1 hour'],
    [604_800, '7 days'],
    [86_400 * 90, '90 days'],
    [90_000, '25 hours'],
  ])('formats %i seconds as %s', (s, text) => {
    expect(formatRetention(s)).toBe(text)
  })

  it('states 24-hour retention in the event explorer', async () => {
    withProject({ ...config(86_400), [EVENTS]: { body: { items: [], next_cursor: null } } })
    renderApp(`/projects/${PID}/events`)
    expect(await screen.findByText('Events are deleted after 24 hours.')).toBeInTheDocument()
  })

  it('states 7-day retention and the privacy notice on the project overview', async () => {
    withProject({ ...config(604_800), [EVENTS]: { body: { items: [], next_cursor: null } } })
    renderApp(`/projects/${PID}`)
    expect(await screen.findByText('Events are deleted after 7 days.')).toBeInTheDocument()
    expect(
      screen.getByText(/may contain confidential or personal data.*stored for 7 days/),
    ).toBeInTheDocument()
  })

  it('shows the privacy notice with the retention period on the endpoint detail', async () => {
    withProject({ ...config(86_400), [`GET ${ENDPOINT}`]: { body: endpoint } })
    renderApp(`/projects/${PID}/endpoints/${EPID}`)
    expect(
      await screen.findByText(/may contain confidential or personal data.*stored for 24 hours, then deleted/),
    ).toBeInTheDocument()
  })

  it('says events are not deleted by age when retention is off (development)', async () => {
    withProject({ ...config(0), [EVENTS]: { body: { items: [], next_cursor: null } } })
    renderApp(`/projects/${PID}/events`)
    expect(
      await screen.findByText('Events are not deleted automatically in this environment.'),
    ).toBeInTheDocument()
  })
})
