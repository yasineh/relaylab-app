// Test fixtures for the replay UI.
import type { Endpoint } from '@/features/endpoints/api'
import type { EventDetail } from '@/features/events/api'
import { endpoint, PID, summary, withProject } from '@/features/events/fixtures'
import type { Preview, Replay } from '@/features/replay/api'
import { signedIn, type Handler } from '@/test/api'

export const EVENT_ID = summary().id
export const SINK_ID = '01936f3e-8c9d-7e1f-a2b3-c4d5e6f70820'
export const EVENT_PATH = `/api/v1/projects/${PID}/events/${EVENT_ID}`
export const REPLAYS = `${EVENT_PATH}/replays`

export const sink: Endpoint = { ...endpoint, id: SINK_ID, name: 'Sink', url: null }

export const detail: EventDetail = {
  ...summary(),
  raw_query: '',
  query: [],
  headers: [
    { name: 'Authorization', value: '[REDACTED]', redacted: true },
    { name: 'Content-Type', value: 'application/json', redacted: false },
  ],
  redacted_count: 1,
  body: { encoding: 'utf8', text: '{"a":1}', is_json: true, json_invalid: false, size: 7 },
  source_ip_prefix: null,
  user_agent: null,
  correlation_id: 'r',
}

export const preview = (over: Partial<Preview> = {}): Preview => ({
  method: 'POST',
  target: 'Test sink: Sink',
  destination: { type: 'sink', endpoint_id: SINK_ID, endpoint_name: 'Sink' },
  headers: [
    { name: 'Content-Type', value: 'application/json', redacted: false },
    { name: 'X-Relaylab-Replay-Id', value: '(assigned when the replay is created)', redacted: false },
  ],
  stripped_headers: ['Authorization', 'Stripe-Signature'],
  body_size: 7,
  content_type: 'application/json',
  ...over,
})

export const replay = (over: Partial<Replay> = {}): Replay => ({
  id: '01937000-0000-7000-8000-0000000000aa',
  event_id: EVENT_ID,
  state: 'queued',
  outcome: null,
  failure_category: null,
  failure_message: null,
  destination: { type: 'sink', endpoint_id: SINK_ID, endpoint_name: 'Sink' },
  target: 'Test sink: Sink',
  method: 'POST',
  tries: 0,
  response_status: null,
  duration_ms: null,
  created_at: '2026-11-03T09:20:00Z',
  finished_at: null,
  ...over,
})

export const fieldError = (status: number, code: string, fields: Record<string, string>) => ({
  status,
  body: { error: { code, message: 'This destination cannot be used.', request_id: 'r', fields } },
})

export function withReplay(
  handlers: Record<string, Handler>,
  config: { enabled?: boolean; url?: boolean } = {},
) {
  return withProject({
    'GET /api/v1/config': {
      body: {
        ...(signedIn['GET /api/v1/config'] as { body: object }).body,
        replay_enabled: config.enabled ?? true,
        replay_url_available: config.url ?? true,
      },
    },
    [`GET /api/v1/projects/${PID}/endpoints`]: { body: { items: [endpoint, sink] } },
    [`GET ${EVENT_PATH}`]: { body: detail },
    [`GET ${REPLAYS}`]: { body: { items: [], next_cursor: null } },
    [`POST ${REPLAYS}/preview`]: { body: preview() },
    ...handlers,
  })
}
