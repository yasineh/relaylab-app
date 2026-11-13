import { act, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'

import { PID } from '@/features/events/fixtures'
import {
  EVENT_ID,
  fieldError,
  preview,
  replay,
  REPLAYS,
  SINK_ID,
  withReplay,
} from '@/features/replay/fixtures'
import { SIGNATURE_NOTICE } from '@/features/replay/ReplayDialog'
import { apiError } from '@/test/api'
import { axeViolations } from '@/test/axe'
import { renderApp } from '@/test/render'

const openDialog = async () => {
  renderApp(`/projects/${PID}/events/${EVENT_ID}`)
  await userEvent.click(await screen.findByRole('button', { name: 'Replay' }))
  return screen.findByRole('dialog', { name: 'Replay event' })
}

describe('replay dialog (6.1)', () => {
  it('preselects the test sink and shows the signature notice', async () => {
    withReplay({})
    const dialog = await openDialog()
    expect(within(dialog).getByRole('radio', { name: 'Test sink' })).toBeChecked()
    expect(within(dialog).getByRole('combobox', { name: 'Sink endpoint' })).toHaveValue(SINK_ID)
    // The event's own endpoint is not offered as its sink.
    expect(within(dialog).getAllByRole('option')).toHaveLength(1 + 5) // 1 sink + 5 methods
    expect(within(dialog).getByText(SIGNATURE_NOTICE)).toBeInTheDocument()
  })

  it('shows the preview before Send replay and sends only on explicit confirmation', async () => {
    let created = 0
    const { calls, spy } = withReplay({
      [`POST ${REPLAYS}`]: () => {
        created++
        return { status: 202, body: replay() }
      },
    })
    const dialog = await openDialog()
    const send = within(dialog).getByRole('button', { name: 'Send replay' })
    expect(send).toBeDisabled()
    expect(await within(dialog).findByText('Test sink: Sink')).toBeInTheDocument()
    expect(within(dialog).getByRole('table', { name: 'Headers that will be sent' })).toHaveTextContent(
      'Content-Type',
    )
    expect(within(dialog).getByText(/Not forwarded/)).toHaveTextContent('Authorization, Stripe-Signature')
    expect(created).toBe(0)
    const previewCall = calls.find((c) => c.path.endsWith('/preview'))!
    expect(previewCall.body).toEqual({ method: 'POST', destination: { type: 'sink', endpoint_id: SINK_ID } })

    await waitFor(() => expect(send).toBeEnabled())
    await userEvent.click(send)
    await waitFor(() => expect(created).toBe(1))
    const init = spy.mock.calls.find(
      ([input, i]) => String(input).endsWith('/replays') && i?.method === 'POST',
    )![1]!
    expect(new Headers(init.headers).get('Idempotency-Key')).toMatch(/^[0-9a-f-]{36}$/)
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
  })

  it('shows server validation next to the URL field and stays open', async () => {
    withReplay({
      [`POST ${REPLAYS}/preview`]: ({ body }) =>
        (body as { destination: { type: string } }).destination.type === 'url'
          ? fieldError(422, 'invalid_destination', {
              'destination.url': 'The destination is, or resolves to, a non-public address.',
            })
          : { body: preview() },
    })
    const dialog = await openDialog()
    await userEvent.click(within(dialog).getByRole('radio', { name: 'URL' }))
    const input = within(dialog).getByRole('textbox', { name: 'Destination URL' })
    await userEvent.type(input, 'https://10.0.0.1/hook')
    const message = await within(dialog).findByText(
      'The destination is, or resolves to, a non-public address.',
    )
    expect(input).toHaveAttribute('aria-invalid', 'true')
    expect(input.getAttribute('aria-describedby')).toBe(message.id)
    expect(within(dialog).getByRole('button', { name: 'Send replay' })).toBeDisabled()
    expect(screen.getByRole('dialog')).toBeInTheDocument()
  })

  it('sends header overrides and shows a 422 from creation inline', async () => {
    const { calls } = withReplay({
      [`POST ${REPLAYS}`]: fieldError(422, 'validation_failed', {
        header_overrides: 'Cookie cannot be set on a replay.',
      }),
    })
    const dialog = await openDialog()
    await userEvent.click(within(dialog).getByRole('button', { name: 'Add header' }))
    await userEvent.type(within(dialog).getByRole('textbox', { name: 'Header name 1' }), 'Authorization')
    await userEvent.type(within(dialog).getByLabelText('Header value 1'), 'Bearer s3cret')
    await waitFor(() =>
      expect(calls.filter((c) => c.path.endsWith('/preview')).at(-1)?.body).toMatchObject({
        header_overrides: { Authorization: 'Bearer s3cret' },
      }),
    )
    await waitFor(() => expect(within(dialog).getByRole('button', { name: 'Send replay' })).toBeEnabled())
    await userEvent.click(within(dialog).getByRole('button', { name: 'Send replay' }))
    expect(await within(dialog).findByText('Cookie cannot be set on a replay.')).toBeInTheDocument()
    expect(within(dialog).getByLabelText('Header value 1')).toHaveAttribute('type', 'password')
  })

  it('is keyboard accessible with labelled fields', async () => {
    withReplay({})
    const dialog = await openDialog()
    await within(dialog).findByText('Test sink: Sink')
    expect(await axeViolations(dialog)).toEqual([])
    await userEvent.keyboard('{Escape}')
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Replay' })).toHaveFocus()
  })
})

describe('replay availability (6.1)', () => {
  it('keeps the test sink available and explains URL replay is off when REPLAY_ENABLED is false', async () => {
    withReplay({}, { enabled: false, url: false })
    const dialog = await openDialog()
    expect(within(dialog).getByRole('radio', { name: 'URL' })).toBeDisabled()
    expect(
      within(dialog).getByText(/Sending to a URL is turned off on this RelayLab server/),
    ).toBeInTheDocument()
    expect(within(dialog).getByRole('radio', { name: 'Test sink' })).toBeChecked()
    await waitFor(() => expect(within(dialog).getByRole('button', { name: 'Send replay' })).toBeEnabled())
  })

  it('disables URL destinations when the outbound guard has not reported in', async () => {
    withReplay({}, { url: false })
    const dialog = await openDialog()
    expect(within(dialog).getByRole('radio', { name: 'URL' })).toBeDisabled()
    expect(within(dialog).getByText(/Sending to a URL is unavailable right now/)).toBeInTheDocument()
    expect(within(dialog).getByRole('radio', { name: 'Test sink' })).toBeChecked()
  })

  it('explains a 503 from the server', async () => {
    withReplay({ [`POST ${REPLAYS}/preview`]: apiError(503, 'replay_unavailable') })
    const dialog = await openDialog()
    expect(await within(dialog).findByText(/Replay is unavailable right now/)).toBeInTheDocument()
    expect(within(dialog).getByRole('button', { name: 'Send replay' })).toBeDisabled()
  })
})

describe('event replay panel (6.2)', () => {
  it('labels states honestly and polls until the replay finishes', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    try {
      const states = [
        replay(),
        replay({ state: 'running', tries: 1 }),
        replay({
          state: 'succeeded',
          outcome: 'succeeded',
          response_status: 204,
          tries: 1,
          finished_at: '2026-11-03T09:20:01Z',
        }),
      ]
      let n = 0
      withReplay({
        [`GET ${REPLAYS}`]: () => ({ body: { items: [states[Math.min(n++, 2)]], next_cursor: null } }),
      })
      renderApp(`/projects/${PID}/events/${EVENT_ID}`)
      const list = await screen.findByRole('list', { name: 'Replays of this event' })
      expect(list).toHaveTextContent('Queued')
      await act(() => vi.advanceTimersByTimeAsync(2_100))
      await waitFor(() => expect(list).toHaveTextContent('Running'))
      await act(() => vi.advanceTimersByTimeAsync(2_100))
      await waitFor(() => expect(list).toHaveTextContent('Succeeded · 204'))
      const calls = n
      await act(() => vi.advanceTimersByTimeAsync(6_000))
      expect(n).toBe(calls) // no polling once terminal
    } finally {
      vi.useRealTimers()
    }
  })

  it('shows a failure with its reason and an empty state', async () => {
    withReplay({
      [`GET ${REPLAYS}`]: {
        body: {
          items: [
            replay({
              state: 'failed',
              outcome: 'blocked_address',
              failure_category: 'blocked_address',
              tries: 1,
            }),
          ],
          next_cursor: null,
        },
      },
    })
    renderApp(`/projects/${PID}/events/${EVENT_ID}`)
    expect(await screen.findByText('Failed · non-public address')).toBeInTheDocument()
  })
})
