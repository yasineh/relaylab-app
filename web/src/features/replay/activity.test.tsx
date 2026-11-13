import { screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'

import { PID } from '@/features/events/fixtures'
import { withReplay } from '@/features/replay/fixtures'
import { formatPreciseDateTime } from '@/lib/format'
import { currentPath, renderApp } from '@/test/render'

const AUDIT = `GET /api/v1/projects/${PID}/audit-log`

describe('project activity (6.4)', () => {
  it('lists entries with action, actor, resource, and local time', async () => {
    withReplay({
      [AUDIT]: {
        body: {
          items: [
            {
              id: 'a1',
              action: 'replay.created',
              actor_user_id: 'u1',
              actor_name: 'Ada Lovelace',
              resource_type: 'replay',
              resource_id: 'r1',
              metadata: { destination_type: 'url', destination_host: 'hooks.example.com', method: 'POST' },
              created_at: '2026-11-03T09:20:00Z',
            },
            {
              id: 'a2',
              action: 'endpoint.created',
              actor_user_id: null,
              actor_name: null,
              resource_type: 'endpoint',
              resource_id: 'e1',
              metadata: { name: 'Stripe staging' },
              created_at: '2026-11-02T09:20:00Z',
            },
          ],
          next_cursor: null,
        },
      },
    })
    const { router } = renderApp(`/projects/${PID}/settings`)
    await userEvent.click(await screen.findByRole('tab', { name: 'Activity' }))
    expect(currentPath(router)).toBe(`/projects/${PID}/settings?tab=activity`)
    const items = within(await screen.findByRole('list', { name: 'Project activity' })).getAllByRole(
      'listitem',
    )
    expect(items[0]).toHaveTextContent('Replay created')
    expect(items[0]).toHaveTextContent('POST to hooks.example.com')
    expect(items[0]).toHaveTextContent('Ada Lovelace')
    expect(items[0]).toHaveTextContent(formatPreciseDateTime('2026-11-03T09:20:00Z'))
    expect(items[1]).toHaveTextContent('Endpoint created')
    expect(items[1]).toHaveTextContent('Deleted user')
  })

  it('shows an empty state', async () => {
    withReplay({ [AUDIT]: { body: { items: [], next_cursor: null } } })
    renderApp(`/projects/${PID}/settings?tab=activity`)
    expect(await screen.findByRole('heading', { name: 'No activity yet' })).toBeInTheDocument()
  })

  it('keeps general settings on the first tab and supports arrow keys', async () => {
    withReplay({ [AUDIT]: { body: { items: [], next_cursor: null } } })
    renderApp(`/projects/${PID}/settings`)
    const general = await screen.findByRole('tab', { name: 'General' })
    expect(general).toHaveAttribute('aria-selected', 'true')
    expect(screen.getByRole('heading', { name: 'Project details' })).toBeInTheDocument()
    general.focus()
    await userEvent.keyboard('{ArrowRight}')
    expect(screen.getByRole('tab', { name: 'Activity' })).toHaveAttribute('aria-selected', 'true')
    expect(await screen.findByRole('heading', { name: 'No activity yet' })).toBeInTheDocument()
  })
})
