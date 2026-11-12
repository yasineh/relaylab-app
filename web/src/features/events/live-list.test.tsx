import { act, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { EventSummary } from '@/features/events/api'
import { EVENTS, PID, summary, withProject } from '@/features/events/fixtures'
import { RECENT_COUNT } from '@/features/events/RecentEvents'
import { FakeEventSource } from '@/test/event-source'
import { renderApp } from '@/test/render'

// UUIDv7-shaped ids: later suffix = newer.
const id = (n: number) => `01937000-0000-7000-8000-${String(n).padStart(12, '0')}`
const ev = (n: number, over: Partial<EventSummary> = {}) =>
  summary({
    id: id(n),
    path: `/event-${n}`,
    received_at: `2026-11-03T09:${String(n).padStart(2, '0')}:00Z`,
    ...over,
  })

const rows = () =>
  within(screen.getByRole('table', { name: 'Received events' }))
    .getAllByRole('row')
    .slice(1)
const expectPaths = (want: string[]) => waitFor(() => expect(paths()).toEqual(want))
const paths = () => rows().map((r) => within(r).getAllByRole('link')[0]!.textContent)

/** The page's stream: open it (this triggers the sync refetch) and return it. */
async function openStream() {
  await waitFor(() => expect(FakeEventSource.instances.length).toBeGreaterThan(0))
  const es = FakeEventSource.last()
  act(() => es.open())
  return es
}

function setScroll(y: number) {
  Object.defineProperty(window, 'scrollY', { value: y, configurable: true })
}

let scrollTo: ReturnType<typeof vi.spyOn>
beforeEach(() => {
  setScroll(0)
  scrollTo = vi.spyOn(window, 'scrollTo').mockImplementation(() => {})
})
afterEach(() => setScroll(0))

describe('live event list (3.2)', () => {
  it('inserts a new event at the top without a page refresh', async () => {
    let listCalls = 0
    withProject({
      [EVENTS]: () => {
        listCalls++
        return { body: { items: [ev(2), ev(1)], next_cursor: null } }
      },
    })
    renderApp(`/projects/${PID}/events`)
    await screen.findByRole('table', { name: 'Received events' })
    const es = await openStream()
    await waitFor(() => expect(listCalls).toBe(2)) // a fresh stream reloads once
    act(() => es.emit('event.created', ev(3), id(3)))
    await expectPaths(['/event-3', '/event-2', '/event-1'])
    // The same event again (for example replayed after a reconnect) is not duplicated.
    act(() => es.emit('event.created', ev(3), id(3)))
    act(() => es.emit('event.created', ev(2), id(2)))
    act(() => es.emit('event.created', ev(4), id(4)))
    await expectPaths(['/event-4', '/event-3', '/event-2', '/event-1'])
    expect(listCalls).toBe(2)
  })

  it('keeps an event that arrives while the list is reloading', async () => {
    let listCalls = 0
    let release!: () => void
    const gate = new Promise<void>((r) => (release = r))
    withProject({
      [EVENTS]: async () => {
        listCalls++
        if (listCalls === 2) await gate // the sync reload, snapshot taken before ev(3)
        return { body: { items: [ev(2), ev(1)], next_cursor: null } }
      },
    })
    renderApp(`/projects/${PID}/events`)
    await screen.findByRole('table', { name: 'Received events' })
    const es = await openStream()
    await waitFor(() => expect(listCalls).toBe(2))
    act(() => es.emit('event.created', ev(3), id(3)))
    await expectPaths(['/event-3', '/event-2', '/event-1'])
    release()
    await waitFor(() => expect(screen.queryByText('/event-3')).toBeInTheDocument())
    // The reload answered without it; it is added back.
    await new Promise((r) => setTimeout(r, 20))
    await expectPaths(['/event-3', '/event-2', '/event-1'])
  })

  it('replaces the empty state when the first event arrives', async () => {
    withProject({ [EVENTS]: { body: { items: [], next_cursor: null } } })
    renderApp(`/projects/${PID}/events`)
    expect(await screen.findByRole('heading', { name: 'No events yet' })).toBeInTheDocument()
    const es = await openStream()
    act(() => es.emit('event.created', ev(1), id(1)))
    expect(await screen.findByRole('table', { name: 'Received events' })).toBeInTheDocument()
    await expectPaths(['/event-1'])
  })

  it('holds new events behind a "Show N new events" button when scrolled down', async () => {
    withProject({ [EVENTS]: { body: { items: [ev(1)], next_cursor: null } } })
    renderApp(`/projects/${PID}/events`)
    await screen.findByRole('table', { name: 'Received events' })
    const es = await openStream()
    setScroll(800)
    act(() => es.emit('event.created', ev(2), id(2)))
    act(() => es.emit('event.created', ev(3), id(3)))
    await expectPaths(['/event-1'])
    await userEvent.click(screen.getByRole('button', { name: 'Show 2 new events' }))
    await expectPaths(['/event-3', '/event-2', '/event-1'])
    expect(scrollTo).toHaveBeenCalledWith({ top: 0 })
    expect(screen.queryByRole('button', { name: /new event/ })).not.toBeInTheDocument()
  })

  it('does not insert events the filters exclude, and says how many arrived', async () => {
    withProject({
      [EVENTS]: { body: { items: [ev(1, { method: 'GET' })], next_cursor: null } },
    })
    renderApp(`/projects/${PID}/events?method=GET`)
    await screen.findByRole('table', { name: 'Received events' })
    const es = await openStream()
    act(() => es.emit('event.created', ev(2, { method: 'POST' }), id(2)))
    await expectPaths(['/event-1'])
    expect(await screen.findByText(/1 new event doesn’t match these filters/)).toBeInTheDocument()
    act(() => es.emit('event.created', ev(3, { method: 'GET' }), id(3)))
    await expectPaths(['/event-3', '/event-1'])
    act(() => es.emit('event.created', ev(4, { method: 'PUT' }), id(4)))
    expect(await screen.findByText(/2 new events don’t match these filters/)).toBeInTheDocument()
  })

  it('reloads instead of inserting when sorted oldest first', async () => {
    let listCalls = 0
    withProject({
      [EVENTS]: () => {
        listCalls++
        return { body: { items: listCalls > 2 ? [ev(1), ev(2)] : [ev(1)], next_cursor: null } }
      },
    })
    renderApp(`/projects/${PID}/events?sort=asc`)
    await screen.findByRole('table', { name: 'Received events' })
    const es = await openStream()
    await waitFor(() => expect(listCalls).toBe(2))
    act(() => es.emit('event.created', ev(2), id(2)))
    await expectPaths(['/event-1'])
    await userEvent.click(screen.getByRole('button', { name: 'Show 1 new event' }))
    await waitFor(() => expect(paths()).toEqual(['/event-1', '/event-2']))
  })

  it('reloads the list after resync.required once the new stream opens', async () => {
    let listCalls = 0
    withProject({
      [EVENTS]: () => {
        listCalls++
        return { body: { items: [ev(1)], next_cursor: null } }
      },
    })
    renderApp(`/projects/${PID}/events`)
    await screen.findByRole('table', { name: 'Received events' })
    const first = await openStream()
    await waitFor(() => expect(listCalls).toBe(2))
    act(() => first.emit('resync.required', { reason: 'too_many_missed' }))
    expect(listCalls).toBe(2) // not before the new stream is subscribed
    act(() => FakeEventSource.last().open())
    await waitFor(() => expect(listCalls).toBe(3))
  })

  it('does not add an older replayed event to the first page when more pages exist', async () => {
    withProject({ [EVENTS]: { body: { items: [ev(9), ev(8)], next_cursor: 'c1' } } })
    renderApp(`/projects/${PID}/events`)
    await screen.findByRole('table', { name: 'Received events' })
    const es = await openStream()
    act(() => es.emit('event.created', ev(7), id(7))) // belongs to page 2
    act(() => es.emit('event.created', ev(10), id(10)))
    await expectPaths(['/event-10', '/event-9', '/event-8'])
    expect(screen.queryByText('/event-7')).not.toBeInTheDocument()
  })
})

describe('live project overview (3.2)', () => {
  it('lists the newest events and adds new ones live', async () => {
    withProject({
      [EVENTS]: { body: { items: [5, 4, 3, 2, 1, 0].map((n) => ev(n)), next_cursor: null } },
    })
    renderApp(`/projects/${PID}`)
    const list = await screen.findByRole('list', { name: 'Recent events' })
    expect(within(list).getAllByRole('listitem')).toHaveLength(RECENT_COUNT)
    expect(within(list).getAllByRole('listitem')[0]).toHaveTextContent('/event-5')
    const es = await openStream()
    act(() => es.emit('event.created', ev(6, { method: 'PUT' }), id(6)))
    await waitFor(() =>
      expect(
        within(screen.getByRole('list', { name: 'Recent events' })).getAllByRole('listitem')[0],
      ).toHaveTextContent('/event-6'),
    )
    const items = within(screen.getByRole('list', { name: 'Recent events' })).getAllByRole('listitem')
    expect(items).toHaveLength(RECENT_COUNT)
    expect(items[0]).toHaveTextContent('PUT')
    expect(within(items[0]!).getByRole('link', { name: '/event-6' })).toHaveAttribute(
      'href',
      `/projects/${PID}/events/${id(6)}`,
    )
    expect(screen.getByRole('link', { name: 'View all events' })).toHaveAttribute(
      'href',
      `/projects/${PID}/events`,
    )
  })

  it('shows an honest empty state that fills in live', async () => {
    withProject({ [EVENTS]: { body: { items: [], next_cursor: null } } })
    renderApp(`/projects/${PID}`)
    expect(await screen.findByText(/No events yet/)).toBeInTheDocument()
    const es = await openStream()
    act(() => es.emit('event.created', ev(1), id(1)))
    expect(await screen.findByRole('list', { name: 'Recent events' })).toHaveTextContent('/event-1')
  })
})
