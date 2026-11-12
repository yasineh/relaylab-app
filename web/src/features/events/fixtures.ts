// Test fixtures shared by the event explorer tests.
import type { Endpoint } from '@/features/endpoints/api'
import type { EventSummary } from '@/features/events/api'
import type { Project } from '@/features/projects/api'
import { mockApi, signedIn, type Handler } from '@/test/api'

export const PID = '01936f3e-7b8a-7c1d-9e2f-0a1b2c3d4e5f'
export const EPID = '01936f3e-8c9d-7e1f-a2b3-c4d5e6f70819'
export const URL1 = 'https://relaylab.test/h/AbCdEfGhIjKlMnOpQrStUvWxYz0123456789abcdefg'

export const project: Project = {
  id: PID,
  name: 'Stripe sandbox',
  description: null,
  created_at: '2026-11-01T10:00:00Z',
  updated_at: '2026-11-01T10:00:00Z',
}

export const endpoint: Endpoint = {
  id: EPID,
  project_id: PID,
  name: 'Stripe staging',
  enabled: true,
  url: URL1,
  last_received_at: null,
  created_at: '2026-11-02T10:00:00Z',
  updated_at: '2026-11-02T10:00:00Z',
}

export const summary = (over: Partial<EventSummary> = {}): EventSummary => ({
  id: '01937000-0000-7000-8000-000000000001',
  endpoint_id: EPID,
  endpoint_name: 'Stripe staging',
  method: 'POST',
  path: '/charge/succeeded',
  content_type: 'application/json',
  body_size: 1536,
  received_at: '2026-11-03T09:15:00Z',
  ...over,
})

export const EVENTS = `GET /api/v1/projects/${PID}/events`

export function withProject(handlers: Record<string, Handler>) {
  return mockApi({
    ...signedIn,
    [`GET /api/v1/projects/${PID}`]: { body: project },
    [`GET /api/v1/projects/${PID}/endpoints`]: { body: { items: [endpoint] } },
    ...handlers,
  })
}
