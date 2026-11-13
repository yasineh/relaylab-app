import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'

import { PID } from '@/features/events/fixtures'
import { replay, withReplay } from '@/features/replay/fixtures'
import { apiError } from '@/test/api'
import { currentPath, renderApp } from '@/test/render'

const LIST = `GET /api/v1/projects/${PID}/replays`
const id = (n: number) => `01937000-0000-7000-8000-0000000000${String(n).padStart(2, '0')}`

describe('replay history (6.3)', () => {
  it('lists replays with state, target, and duration, and loads more', async () => {
    withReplay({
      [LIST]: ({ url }) =>
        url.searchParams.get('cursor') === 'c1'
          ? { body: { items: [replay({ id: id(3), target: 'https://third.example/' })], next_cursor: null } }
          : {
              body: {
                items: [
                  replay({
                    id: id(1),
                    state: 'succeeded',
                    outcome: 'succeeded',
                    response_status: 200,
                    duration_ms: 42,
                  }),
                  replay({
                    id: id(2),
                    state: 'failed',
                    outcome: 'timeout',
                    failure_category: 'timeout',
                    target: 'https://slow.example/',
                  }),
                ],
                next_cursor: 'c1',
              },
            },
    })
    renderApp(`/projects/${PID}/replays`)
    const table = await screen.findByRole('table', { name: 'Replays' })
    const rows = within(table).getAllByRole('row').slice(1)
    expect(rows[0]).toHaveTextContent('Succeeded · 200')
    expect(rows[0]).toHaveTextContent('42 ms')
    expect(rows[1]).toHaveTextContent('Failed · timed out')
    expect(rows[1]).toHaveTextContent('POST https://slow.example/')
    expect(screen.getByRole('link', { name: 'Replays' })).toHaveAttribute('aria-current', 'page')
    await userEvent.click(screen.getByRole('button', { name: 'Load more' }))
    await waitFor(() => expect(within(table).getAllByRole('row')).toHaveLength(4))
    expect(screen.getByText('No more replays.')).toBeInTheDocument()
  })

  it('filters by state through the URL', async () => {
    const { calls } = withReplay({ [LIST]: { body: { items: [replay()], next_cursor: null } } })
    const { router } = renderApp(`/projects/${PID}/replays`)
    await screen.findByRole('table', { name: 'Replays' })
    await userEvent.selectOptions(screen.getByRole('combobox', { name: 'State' }), 'failed')
    await waitFor(() => expect(currentPath(router)).toBe(`/projects/${PID}/replays?state=failed`))
    await waitFor(() =>
      expect(calls.some((c) => c.path.endsWith('/replays') && c.method === 'GET')).toBe(true),
    )
  })

  it('shows an empty state that explains how to replay', async () => {
    withReplay({ [LIST]: { body: { items: [], next_cursor: null } } })
    renderApp(`/projects/${PID}/replays`)
    expect(await screen.findByRole('heading', { name: 'No replays yet' })).toBeInTheDocument()
    expect(screen.getByText(/choose/)).toHaveTextContent('Open an event and choose Replay')
  })

  it('shows loading and error states', async () => {
    let release!: () => void
    const gate = new Promise<void>((r) => (release = r))
    withReplay({ [LIST]: async () => (await gate, apiError(500, 'internal_error')) })
    renderApp(`/projects/${PID}/replays`)
    expect(await screen.findByText('Loading replays…')).toBeInTheDocument()
    release()
    expect(await screen.findByText('Replays could not be loaded.')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Try again' })).toBeInTheDocument()
  })

  it('renders an HTML response excerpt as literal text in the detail', async () => {
    const html = '<script>window.pwned = true</script><img src=x onerror="window.pwned=true">'
    withReplay({
      [LIST]: { body: { items: [replay({ id: id(1) })], next_cursor: null } },
      [`GET /api/v1/projects/${PID}/replays/${id(1)}`]: {
        body: replay({
          id: id(1),
          state: 'succeeded',
          outcome: 'succeeded',
          response_status: 200,
          tries: 1,
          duration_ms: 12,
          header_overrides: [{ name: 'Authorization', value: '[REDACTED]', redacted: true }],
          response: {
            status: 200,
            headers: [
              { name: 'Content-Type', value: 'text/html', redacted: false },
              { name: 'Set-Cookie', value: '[REDACTED]', redacted: true },
            ],
            body: { encoding: 'utf8', text: html, size: html.length, truncated: true },
          },
        }),
      },
    })
    renderApp(`/projects/${PID}/replays?replay=${id(1)}`)
    const dialog = await screen.findByRole('dialog', { name: 'Replay details' })
    const pre = await within(dialog).findByLabelText('Response body')
    expect(pre.textContent).toBe(html)
    expect(dialog.querySelector('script, img')).toBeNull()
    expect((window as { pwned?: boolean }).pwned).toBeUndefined()
    expect(within(dialog).getByText(/truncated/)).toBeInTheDocument()
    expect(within(dialog).getByText('Authorization: [REDACTED]')).toBeInTheDocument()
    expect(within(dialog).getByRole('table', { name: 'Response headers' })).toHaveTextContent('[REDACTED]')
  })

  it('shows a failure category and its safe message', async () => {
    withReplay({
      [LIST]: { body: { items: [], next_cursor: null } },
      [`GET /api/v1/projects/${PID}/replays/${id(2)}`]: {
        body: replay({
          id: id(2),
          state: 'failed',
          outcome: 'blocked_address',
          failure_category: 'blocked_address',
          failure_message: 'The destination is, or resolves to, a non-public address.',
          tries: 1,
        }),
      },
    })
    renderApp(`/projects/${PID}/replays?replay=${id(2)}`)
    const dialog = await screen.findByRole('dialog', { name: 'Replay details' })
    expect(await within(dialog).findByRole('alert')).toHaveTextContent(
      'non-public address. The destination is, or resolves to, a non-public address.',
    )
  })
})
