import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import { apiFetch } from '@/lib/api'

export type ReplayState = 'queued' | 'running' | 'succeeded' | 'failed'
export const TERMINAL: ReplayState[] = ['succeeded', 'failed']
export const REPLAY_METHODS = ['GET', 'POST', 'PUT', 'PATCH', 'DELETE'] as const

export type Destination = {
  type: 'url' | 'sink'
  url?: string
  endpoint_id?: string
  endpoint_name?: string
}

/** A header as shown to users; override values are always [REDACTED]. */
export type ShownHeader = { name: string; value: string; redacted: boolean }

export type Replay = {
  id: string
  event_id: string
  state: ReplayState
  outcome: string | null
  failure_category: string | null
  failure_message: string | null
  destination: Destination
  target: string
  method: string
  tries: number
  response_status: number | null
  duration_ms: number | null
  created_at: string
  started_at?: string
  finished_at: string | null
  header_overrides?: ShownHeader[]
  response?: {
    status: number
    headers: ShownHeader[]
    body: { encoding: 'utf8' | 'binary'; text?: string; base64?: string; size: number; truncated: boolean }
  }
}

export type ReplayPage = { items: Replay[]; next_cursor: string | null }

export type ReplayInput = {
  destination: { type: 'url'; url: string } | { type: 'sink'; endpoint_id: string }
  method: string
  header_overrides?: Record<string, string>
}

export type Preview = {
  method: string
  target: string
  destination: Destination
  headers: ShownHeader[]
  stripped_headers: string[]
  body_size: number
  content_type: string | null
}

export type AuditEntry = {
  id: string
  action: string
  actor_user_id: string | null
  actor_name: string | null
  resource_type: string
  resource_id: string | null
  metadata: Record<string, string>
  created_at: string
}

const enc = encodeURIComponent
const eventReplays = (projectId: string, eventId: string) =>
  `/api/v1/projects/${enc(projectId)}/events/${enc(eventId)}/replays`
const projectReplays = (projectId: string) => `/api/v1/projects/${enc(projectId)}/replays`

export const replaysKey = (projectId: string) => ['replays', projectId] as const
export const eventReplaysKey = (projectId: string, eventId: string) =>
  ['replays', projectId, 'event', eventId] as const

/** Polling interval while any visible replay is not finished (design: 2 s). */
export const POLL_MS = 2_000

const pollWhileActive = (items: Replay[] | undefined) =>
  items?.some((r) => !TERMINAL.includes(r.state)) ? POLL_MS : false

export const isTerminal = (r: Replay) => TERMINAL.includes(r.state)

export function useEventReplays(projectId: string, eventId: string) {
  return useQuery({
    queryKey: eventReplaysKey(projectId, eventId),
    queryFn: ({ signal }) => apiFetch<ReplayPage>(`${eventReplays(projectId, eventId)}?limit=10`, { signal }),
    refetchInterval: (q) => pollWhileActive(q.state.data?.items),
  })
}

export type ReplayFilters = { state?: ReplayState; event_id?: string }

export function useReplays(projectId: string, filters: ReplayFilters) {
  return useInfiniteQuery({
    queryKey: [...replaysKey(projectId), 'list', filters] as const,
    initialPageParam: null as string | null,
    queryFn: ({ pageParam, signal }) => {
      const p = new URLSearchParams()
      if (filters.state) p.set('state', filters.state)
      if (filters.event_id) p.set('event_id', filters.event_id)
      if (pageParam) p.set('cursor', pageParam)
      const qs = p.toString()
      return apiFetch<ReplayPage>(projectReplays(projectId) + (qs ? `?${qs}` : ''), { signal })
    },
    getNextPageParam: (last) => last.next_cursor,
    refetchInterval: (q) => pollWhileActive(q.state.data?.pages.flatMap((p) => p.items)),
  })
}

export function useReplay(projectId: string, replayId: string | null) {
  return useQuery({
    queryKey: [...replaysKey(projectId), 'detail', replayId] as const,
    queryFn: ({ signal }) => apiFetch<Replay>(`${projectReplays(projectId)}/${enc(replayId!)}`, { signal }),
    enabled: replayId !== null,
    refetchInterval: (q) => (q.state.data && !isTerminal(q.state.data) ? POLL_MS : false),
  })
}

export function usePreviewReplay(projectId: string, eventId: string) {
  return useMutation({
    mutationFn: (input: ReplayInput) =>
      apiFetch<Preview>(`${eventReplays(projectId, eventId)}/preview`, {
        method: 'POST',
        body: JSON.stringify(input),
      }),
  })
}

export function useCreateReplay(projectId: string, eventId: string) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: ({ input, idempotencyKey }: { input: ReplayInput; idempotencyKey: string }) =>
      apiFetch<Replay>(eventReplays(projectId, eventId), {
        method: 'POST',
        body: JSON.stringify(input),
        headers: { 'Idempotency-Key': idempotencyKey },
      }),
    onSuccess: () => void client.invalidateQueries({ queryKey: replaysKey(projectId) }),
  })
}

export function useAuditLog(projectId: string) {
  return useInfiniteQuery({
    queryKey: ['audit-log', projectId] as const,
    initialPageParam: null as string | null,
    queryFn: ({ pageParam, signal }) =>
      apiFetch<{ items: AuditEntry[]; next_cursor: string | null }>(
        `/api/v1/projects/${enc(projectId)}/audit-log${pageParam ? `?cursor=${enc(pageParam)}` : ''}`,
        { signal },
      ),
    getNextPageParam: (last) => last.next_cursor,
  })
}

/** Honest labels for states (spec: queued, running, succeeded with status, failed with reason). */
export function stateLabel(r: Replay): string {
  switch (r.state) {
    case 'queued':
      return 'Queued'
    case 'running':
      return 'Running'
    case 'succeeded':
      return r.response_status ? `Succeeded · ${r.response_status}` : 'Succeeded'
    case 'failed':
      return `Failed · ${categoryLabel(r.failure_category)}`
  }
}

const CATEGORY_LABELS: Record<string, string> = {
  invalid_url: 'invalid URL',
  blocked_scheme: 'scheme not allowed',
  blocked_port: 'port not allowed',
  blocked_host: 'host not allowed',
  blocked_address: 'non-public address',
  dns_failure: 'DNS lookup failed',
  connect_failure: 'connection failed',
  tls_failure: 'TLS error',
  timeout: 'timed out',
  response_too_large: 'response too large',
  capacity_exceeded: 'too busy',
  invalid_request: 'invalid request',
  guard_inactive: 'safety check inactive',
  sink_disabled: 'sink disabled',
  worker_lost: 'worker lost',
  override_unavailable: 'headers unreadable',
}

export const categoryLabel = (c: string | null) =>
  c ? (CATEGORY_LABELS[c] ?? c.replaceAll('_', ' ')) : 'unknown'
