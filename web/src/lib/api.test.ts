import { describe, expect, it } from 'vitest'

import { ApiError, apiFetch } from '@/lib/api'
import { mockApi } from '@/test/api'

describe('apiFetch', () => {
  it('parses JSON success bodies and sends JSON headers', async () => {
    const { calls } = mockApi({ 'POST /api/v1/things': { body: { ok: true } } })
    await expect(apiFetch('/api/v1/things', { method: 'POST', body: '{"a":1}' })).resolves.toEqual({
      ok: true,
    })
    expect(calls[0]).toMatchObject({ method: 'POST', path: '/api/v1/things', body: { a: 1 } })
  })

  it('turns the error envelope into an ApiError', async () => {
    mockApi({
      'GET /api/v1/me': {
        status: 401,
        body: { error: { code: 'session_expired', message: 'Expired.', request_id: 'req-1' } },
      },
    })
    const err = await apiFetch('/api/v1/me').catch((e: unknown) => e)
    expect(err).toBeInstanceOf(ApiError)
    expect(err).toMatchObject({
      status: 401,
      code: 'session_expired',
      message: 'Expired.',
      requestId: 'req-1',
    })
  })

  it('uses a generic message when the body is not an envelope', async () => {
    mockApi({ 'GET /api/v1/x': { status: 502, body: 'Bad gateway' } })
    const err = (await apiFetch('/api/v1/x').catch((e: unknown) => e)) as ApiError
    expect(err.code).toBe('http_error')
    expect(err.message).toBe('Something went wrong. Try again.')
  })
})
