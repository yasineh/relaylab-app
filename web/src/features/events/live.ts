import { useQueryClient, type InfiniteData, type QueryClient } from '@tanstack/react-query'
import { useEffect, useRef, useState } from 'react'

import { eventsKey, type EventFilters, type EventPage, type EventSummary } from '@/features/events/api'
import { EventStreamClient, streamUrl, type StreamState } from '@/features/events/stream'
import { projectKey } from '@/features/projects/api'
import { apiFetch, isApiError } from '@/lib/api'

export type LiveHandlers = {
  /** A new event arrived (live or replayed after a reconnect). */
  onEvent: (event: EventSummary) => void
  /** A fresh stream opened; lists should reload (see StreamHandlers.onSync). */
  onSync?: () => void
}

/**
 * Keeps a live event stream open for the project while the component is
 * mounted and returns its connection state.
 */
export function useEventStream(projectId: string, handlers: LiveHandlers): StreamState {
  const queryClient = useQueryClient()
  const [state, setState] = useState<StreamState>('connecting')
  const latest = useRef(handlers)
  useEffect(() => {
    latest.current = handlers
  })

  useEffect(() => {
    let client: EventStreamClient | null = null
    // EventSource hides why a connection failed. Ask the REST API: a 401
    // goes through the app's "session expired" redirect (QueryCache onError),
    // and a 404 marks the project as gone so the layout says so.
    const probe = async () => {
      try {
        await queryClient.fetchQuery({
          queryKey: ['stream-probe', projectId],
          queryFn: ({ signal }) => apiFetch(`/api/v1/projects/${encodeURIComponent(projectId)}`, { signal }),
          staleTime: 0,
          gcTime: 0,
          retry: false,
        })
        return true
      } catch (err) {
        if (isApiError(err) && err.status === 404) {
          void queryClient.invalidateQueries({ queryKey: projectKey(projectId) })
        }
        return false
      }
    }
    client = new EventStreamClient(streamUrl(projectId), {
      onState: setState,
      onEvent: (e) => latest.current.onEvent(e),
      onSync: () => latest.current.onSync?.(),
      onRejected: () => void probe(),
      onSessionExpired: () => {
        setState('offline')
        // Normally the probe gets a 401 and the app redirects to sign-in. If
        // the session turns out to be fine (a race), keep streaming.
        void probe().then((ok) => {
          if (ok) client?.start()
        })
      },
    })
    client.start()
    const online = () => client?.retryNow()
    window.addEventListener('online', online)
    return () => {
      window.removeEventListener('online', online)
      client?.stop()
      client = null
    }
  }, [projectId, queryClient])

  return state
}

/**
 * Whether a streamed event belongs in a list with these filters. It mirrors
 * the server's list filters on the summary fields.
 */
export function matchesFilters(e: EventSummary, f: EventFilters): boolean {
  if (f.endpoint_id && e.endpoint_id !== f.endpoint_id) return false
  if (f.method?.length && !f.method.includes(e.method)) return false
  const at = Date.parse(e.received_at)
  if (f.received_after && at < Date.parse(f.received_after)) return false
  if (f.received_before && at >= Date.parse(f.received_before)) return false
  if (f.content_type) {
    // The server matches a case-insensitive prefix.
    if (!e.content_type?.toLowerCase().startsWith(f.content_type.toLowerCase())) return false
  }
  return true
}

/**
 * Adds events to the cached newest-first list for these filters, keeping
 * newest-first order and skipping ids already loaded. Returns how many were
 * added. Lists that are not cached are left alone (they load fresh).
 */
export function insertLiveEvents(
  client: QueryClient,
  projectId: string,
  filters: EventFilters,
  events: EventSummary[],
): number {
  let added = 0
  client.setQueryData<InfiniteData<EventPage, string | null>>(eventsKey(projectId, filters), (data) => {
    if (!data || data.pages.length === 0) return data
    const known = new Set(data.pages.flatMap((p) => p.items.map((i) => i.id)))
    const [first, ...rest] = data.pages
    // When more pages exist, an event older than the first page's last row
    // belongs to a later page; adding it here would duplicate it there.
    const floor = first!.next_cursor ? first!.items.at(-1)?.id : undefined
    const fresh = events.filter(
      (e) => !known.has(e.id) && (floor === undefined || e.id > floor) && (known.add(e.id), true),
    )
    if (fresh.length === 0) return data
    added = fresh.length
    // UUIDv7 ids sort by time; the list is ordered by id, newest first.
    const items = [...fresh, ...first!.items].sort((a, b) => (a.id < b.id ? 1 : a.id > b.id ? -1 : 0))
    return { ...data, pages: [{ ...first!, items }, ...rest] }
  })
  return added
}
