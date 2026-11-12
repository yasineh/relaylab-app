import { QueryClientProvider } from '@tanstack/react-query'
import { act, renderHook, waitFor } from '@testing-library/react'
import type { ReactNode } from 'react'
import { describe, expect, it, vi } from 'vitest'

import { createQueryClient } from '@/app/App'
import type { EventSummary } from '@/features/events/api'
import { PID, project, summary } from '@/features/events/fixtures'
import { useEventStream } from '@/features/events/live'
import { projectKey } from '@/features/projects/api'
import { apiError, mockApi } from '@/test/api'
import { FakeEventSource } from '@/test/event-source'

const PROJECT = `GET /api/v1/projects/${PID}`

function renderStream() {
  const onEvent = vi.fn<(e: EventSummary) => void>()
  const onSync = vi.fn<() => void>()
  const onSessionLost = vi.fn<() => void>()
  const client = createQueryClient(onSessionLost)
  const wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  )
  const hook = renderHook(() => useEventStream(PID, { onEvent, onSync }), { wrapper })
  return { ...hook, client, onSessionLost, onEvent, onSync }
}

describe('useEventStream (3.1)', () => {
  it('reports the connection state and passes events and syncs through', () => {
    mockApi({})
    const { result, onEvent, onSync } = renderStream()
    expect(result.current).toBe('connecting')
    act(() => FakeEventSource.last().open())
    expect(result.current).toBe('live')
    expect(onSync).toHaveBeenCalledOnce()
    act(() => FakeEventSource.last().emit('event.created', summary(), summary().id))
    expect(onEvent).toHaveBeenCalledWith(summary())
    act(() => FakeEventSource.last().fail())
    expect(result.current).toBe('reconnecting')
  })

  it('sends the user to sign-in when the server says the session expired', async () => {
    mockApi({ [PROJECT]: apiError(401, 'session_expired') })
    const { result, onSessionLost } = renderStream()
    act(() => FakeEventSource.last().open())
    act(() => FakeEventSource.last().emit('session.expired', { reason: 'session_ended' }))
    expect(result.current).toBe('offline')
    await waitFor(() => expect(onSessionLost).toHaveBeenCalled())
    expect(FakeEventSource.open()).toHaveLength(0) // no reconnect
  })

  it('keeps streaming if the session turns out to be valid', async () => {
    mockApi({ [PROJECT]: { body: project } })
    const { onSessionLost } = renderStream()
    const first = FakeEventSource.last()
    act(() => first.open())
    act(() => first.emit('event.created', summary({ id: 'e5' }), 'e5'))
    act(() => first.emit('session.expired', { reason: 'session_ended' }))
    await waitFor(() => expect(FakeEventSource.instances).toHaveLength(2))
    expect(FakeEventSource.last().url).toContain('last_event_id=e5')
    expect(onSessionLost).not.toHaveBeenCalled()
  })

  it('checks the session when a connection is refused and redirects on 401', async () => {
    mockApi({ [PROJECT]: apiError(401, 'unauthenticated') })
    const { onSessionLost } = renderStream()
    act(() => FakeEventSource.last().fail()) // refused before opening
    await waitFor(() => expect(onSessionLost).toHaveBeenCalled())
  })

  it('marks the project as gone when a refused connection finds it deleted', async () => {
    mockApi({ [PROJECT]: apiError(404, 'not_found') })
    const { client, onSessionLost } = renderStream()
    const invalidate = vi.spyOn(client, 'invalidateQueries')
    act(() => FakeEventSource.last().fail())
    await waitFor(() => expect(invalidate).toHaveBeenCalledWith({ queryKey: projectKey(PID) }))
    expect(onSessionLost).not.toHaveBeenCalled()
  })

  it('closes the stream on unmount', () => {
    mockApi({})
    const { unmount } = renderStream()
    act(() => FakeEventSource.last().open())
    unmount()
    expect(FakeEventSource.open()).toHaveLength(0)
  })
})
