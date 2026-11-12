import { act, render, screen, waitFor } from '@testing-library/react'
import { describe, expect, it } from 'vitest'

import { ConnectionStatus } from '@/features/events/ConnectionStatus'
import { EVENTS, PID, withProject } from '@/features/events/fixtures'
import type { StreamState } from '@/features/events/stream'
import { axeViolations } from '@/test/axe'
import { FakeEventSource } from '@/test/event-source'
import { renderApp } from '@/test/render'

const status = (label: string) => screen.getByText(label).closest('[role="status"]')

describe('connection indicator (3.3)', () => {
  it.each([
    ['connecting', 'Connecting…'],
    ['live', 'Live'],
    ['reconnecting', 'Reconnecting…'],
    ['offline', 'Offline'],
  ] satisfies [StreamState, string][])('%s shows "%s" as text, not colour alone', async (state, label) => {
    const { container } = render(<ConnectionStatus state={state} />)
    const el = status(label)
    expect(el).toHaveAttribute('aria-live', 'polite')
    // The coloured dot is decorative; the label and an explanation are text.
    expect(el?.querySelector('[aria-hidden="true"]')).toHaveClass('rounded-full')
    expect(el?.querySelector('.sr-only')?.textContent).toBeTruthy()
    expect(await axeViolations(container)).toEqual([])
  })

  it('follows the stream on the events page: Connecting, Live, Reconnecting, Live', async () => {
    withProject({ [EVENTS]: { body: { items: [], next_cursor: null } } })
    renderApp(`/projects/${PID}/events`)
    expect(await screen.findByText('Connecting…')).toBeInTheDocument()
    await waitFor(() => expect(FakeEventSource.instances).toHaveLength(1))
    act(() => FakeEventSource.last().open())
    expect(await screen.findByText('Live')).toBeInTheDocument()
    act(() => FakeEventSource.last().fail())
    expect(await screen.findByText('Reconnecting…')).toBeInTheDocument()
    expect(screen.queryByText('Live')).not.toBeInTheDocument()
    // The same live region element updates in place, so it is announced.
    const region = status('Reconnecting…')
    act(() => FakeEventSource.last().fail()) // still connecting
    expect(status('Reconnecting…')).toBe(region)
  })

  it('appears on the project overview', async () => {
    withProject({ [EVENTS]: { body: { items: [], next_cursor: null } } })
    renderApp(`/projects/${PID}`)
    await waitFor(() => expect(FakeEventSource.instances).toHaveLength(1))
    act(() => FakeEventSource.last().open())
    expect(await screen.findByText('Live')).toBeInTheDocument()
  })
})
