import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import { apiFetch } from '@/lib/api'

/** One row of GET /api/v1/projects/{projectId}/events (event-explorer spec). */
export type EventSummary = {
  id: string
  endpoint_id: string
  endpoint_name: string
  method: string
  path: string
  content_type: string | null
  body_size: number
  received_at: string
}

/** A header or query value. `redacted` means it was removed when received and is not stored. */
export type Entry = { name: string; value: string; redacted: boolean }

export type EventBody = {
  encoding: 'utf8' | 'binary'
  text?: string
  base64?: string
  /** The body was valid JSON with a JSON content type at receipt. */
  is_json: boolean
  /** A JSON content type, but the body was not valid JSON. */
  json_invalid: boolean
  size: number
}

export type EventDetail = EventSummary & {
  raw_query: string
  query: Entry[]
  headers: Entry[]
  redacted_count: number
  body: EventBody
  source_ip_prefix: string | null
  user_agent: string | null
  correlation_id: string
}

export type EventPage = { items: EventSummary[]; next_cursor: string | null }

export const METHODS = ['GET', 'POST', 'PUT', 'PATCH', 'DELETE', 'HEAD', 'OPTIONS'] as const

/** List filters, as kept in the URL. Times are ISO strings in UTC. */
export type EventFilters = {
  endpoint_id?: string
  method?: string[]
  received_after?: string
  received_before?: string
  content_type?: string
  sort?: 'asc' | 'desc'
}

export const hasFilters = (f: EventFilters) =>
  Boolean(f.endpoint_id || f.method?.length || f.received_after || f.received_before || f.content_type)

/** Reads filters from URL search params, ignoring anything unrecognised. */
export function filtersFromParams(p: URLSearchParams): EventFilters {
  const f: EventFilters = {}
  const get = (k: string) => p.get(k)?.trim() || undefined
  f.endpoint_id = get('endpoint_id')
  const methods = (get('method') ?? '')
    .split(',')
    .map((m) => m.trim().toUpperCase())
    .filter((m): m is (typeof METHODS)[number] => (METHODS as readonly string[]).includes(m))
  if (methods.length) f.method = [...new Set(methods)]
  f.received_after = get('received_after')
  f.received_before = get('received_before')
  f.content_type = get('content_type')
  if (p.get('sort') === 'asc') f.sort = 'asc'
  return f
}

/** Writes filters to URL search params (only the ones that are set). */
export function filtersToParams(f: EventFilters): URLSearchParams {
  const p = new URLSearchParams()
  if (f.endpoint_id) p.set('endpoint_id', f.endpoint_id)
  if (f.method?.length) p.set('method', f.method.join(','))
  if (f.received_after) p.set('received_after', f.received_after)
  if (f.received_before) p.set('received_before', f.received_before)
  if (f.content_type) p.set('content_type', f.content_type)
  if (f.sort === 'asc') p.set('sort', 'asc')
  return p
}

const base = (projectId: string) => `/api/v1/projects/${encodeURIComponent(projectId)}/events`

export const eventPath = (projectId: string, eventId: string) =>
  `${base(projectId)}/${encodeURIComponent(eventId)}`
export const eventBodyPath = (projectId: string, eventId: string) => `${eventPath(projectId, eventId)}/body`

export const eventsKey = (projectId: string, f: EventFilters) => ['events', projectId, f] as const
export const eventKey = (projectId: string, eventId: string) => ['event', projectId, eventId] as const

export function useEvents(projectId: string, filters: EventFilters) {
  return useInfiniteQuery({
    queryKey: eventsKey(projectId, filters),
    initialPageParam: null as string | null,
    queryFn: ({ pageParam, signal }) => {
      const p = filtersToParams(filters)
      if (pageParam) p.set('cursor', pageParam)
      const qs = p.toString()
      return apiFetch<EventPage>(base(projectId) + (qs ? `?${qs}` : ''), { signal })
    },
    getNextPageParam: (last) => last.next_cursor,
  })
}

export function useEvent(projectId: string, eventId: string) {
  return useQuery({
    queryKey: eventKey(projectId, eventId),
    queryFn: ({ signal }) => apiFetch<EventDetail>(eventPath(projectId, eventId), { signal }),
  })
}

/** Permanently deletes one event and its replays (204). */
export function useDeleteEvent(projectId: string, eventId: string) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: () => apiFetch<void>(eventPath(projectId, eventId), { method: 'DELETE' }),
    onSuccess: () => {
      client.removeQueries({ queryKey: eventKey(projectId, eventId) })
      void client.invalidateQueries({ queryKey: ['events', projectId] })
      void client.invalidateQueries({ queryKey: ['replays', projectId] })
    },
  })
}

const clearPath = (projectId: string, endpointId: string) =>
  `/api/v1/projects/${encodeURIComponent(projectId)}/endpoints/${encodeURIComponent(endpointId)}/events`

/**
 * Deletes every event of an endpoint. The server works in time-boxed batches
 * and reports how many it deleted; this repeats until it reports 0 and
 * resolves with the total.
 */
export function useClearEndpointEvents(projectId: string, endpointId: string) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async () => {
      let total = 0
      for (;;) {
        const { deleted } = await apiFetch<{ deleted: number }>(clearPath(projectId, endpointId), {
          method: 'DELETE',
        })
        if (deleted === 0) return total
        total += deleted
      }
    },
    onSettled: () => {
      void client.invalidateQueries({ queryKey: ['events', projectId] })
      void client.invalidateQueries({ queryKey: ['event', projectId] })
      void client.invalidateQueries({ queryKey: ['replays', projectId] })
    },
  })
}
