import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'

import type { EventDetail } from '@/features/events/api'
import { endpoint, EPID, EVENTS, PID, summary, withProject } from '@/features/events/fixtures'
import { axeViolations } from '@/test/axe'
import { currentPath, renderApp } from '@/test/render'

const ID = summary().id
const detail: EventDetail = {
  ...summary(),
  raw_query: 'token=%5BREDACTED%5D',
  query: [{ name: 'token', value: '[REDACTED]', redacted: true }],
  headers: [
    { name: 'Authorization', value: '[REDACTED]', redacted: true },
    { name: 'Content-Type', value: 'application/json', redacted: false },
  ],
  redacted_count: 2,
  body: {
    encoding: 'utf8',
    text: '{"a":{"b":[1,2,{"c":"x"}]}}',
    is_json: true,
    json_invalid: false,
    size: 27,
  },
  source_ip_prefix: null,
  user_agent: null,
  correlation_id: 'r',
}

describe('event explorer accessibility (4.4)', () => {
  it('the events page has no axe violations and a keyboard-operable filter bar and table', async () => {
    const user = userEvent.setup()
    withProject({
      [EVENTS]: { body: { items: [summary(), summary({ id: 'e2', path: '/second' })], next_cursor: 'c1' } },
    })
    const { router } = renderApp(`/projects/${PID}/events`)
    await screen.findByRole('table', { name: 'Received events' })
    expect(await axeViolations()).toEqual([])

    // Every filter control has an accessible name.
    const form = screen.getByRole('form', { name: 'Filter events' })
    for (const name of [
      'Endpoint',
      'Received after',
      'Received before',
      'Content type starts with',
      'Order',
    ]) {
      expect(within(form).getByLabelText(name)).toBeInTheDocument()
    }
    expect(within(form).getByRole('group', { name: 'Methods' })).toBeInTheDocument()

    // Tab order: endpoint select, then the method checkboxes; Space toggles one.
    within(form).getByLabelText('Endpoint').focus()
    await user.tab()
    expect(within(form).getByRole('checkbox', { name: 'GET' })).toHaveFocus()
    await user.keyboard(' ')
    expect(within(form).getByRole('checkbox', { name: 'GET' })).toBeChecked()

    // Each row's link is reachable and opens with Enter.
    const link = within(screen.getByRole('table', { name: 'Received events' })).getByRole('link', {
      name: '/second',
    })
    link.focus()
    await user.keyboard('{Enter}')
    await waitFor(() => expect(currentPath(router)).toBe(`/projects/${PID}/events/e2`))
  })

  it('the empty and no-results states have no axe violations', async () => {
    withProject({ [EVENTS]: { body: { items: [], next_cursor: null } } })
    const { unmount } = renderApp(`/projects/${PID}/events`)
    await screen.findByRole('heading', { name: 'No events yet' })
    expect(await axeViolations()).toEqual([])
    unmount()
    renderApp(`/projects/${PID}/events?method=PUT`)
    await screen.findByRole('heading', { name: 'No events match these filters' })
    expect(await axeViolations()).toEqual([])
  })

  it('every detail tab has no axe violations', async () => {
    const user = userEvent.setup()
    withProject({
      [`GET /api/v1/projects/${PID}/events/${ID}`]: { body: detail },
      [`GET /api/v1/projects/${PID}/endpoints/${EPID}`]: { body: endpoint },
    })
    renderApp(`/projects/${PID}/events/${ID}`)
    await screen.findByRole('tablist', { name: 'Event details' })
    for (const tab of screen.getAllByRole('tab')) {
      await user.click(tab)
      expect(tab).toHaveAttribute('aria-selected', 'true')
      expect(screen.getByRole('tabpanel')).toHaveAccessibleName(tab.textContent ?? '')
      expect(await axeViolations(), `tab ${tab.textContent}`).toEqual([])
    }
    // The JSON tree's toggles are buttons operable with the keyboard.
    await user.click(screen.getByRole('tab', { name: 'Body' }))
    const a = screen.getByRole('button', { name: /"a"/ })
    a.focus()
    await user.keyboard('{Enter}')
    expect(a).toHaveAttribute('aria-expanded', 'false')
    await user.keyboard(' ')
    expect(screen.getByRole('button', { name: /"a"/ })).toHaveAttribute('aria-expanded', 'true')
  })
})

describe('axe helper', () => {
  it('reports real violations in jsdom', async () => {
    // Deliberately inaccessible markup, built without JSX so lint does not reject it.
    const root = document.createElement('div')
    root.append(
      Object.assign(document.createElement('img'), { src: 'data:,' }),
      document.createElement('button'),
    )
    document.body.append(root)
    const found = await axeViolations(root)
    root.remove()
    expect(found.some((v) => v.startsWith('image-alt'))).toBe(true)
    expect(found.some((v) => v.startsWith('button-name'))).toBe(true)
  })
})
