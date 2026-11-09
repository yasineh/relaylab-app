import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'

import type { Project } from '@/features/projects/api'
import { apiError, mockApi, signedIn, type MockReply } from '@/test/api'
import { currentPath, renderApp } from '@/test/render'

const ID = '01936f3e-7b8a-7c1d-9e2f-0a1b2c3d4e5f'

const project = (over: Partial<Project> = {}): Project => ({
  id: ID,
  name: 'Stripe sandbox',
  description: 'Payment provider test hooks',
  created_at: '2026-11-01T10:00:00Z',
  updated_at: '2026-11-02T10:00:00Z',
  ...over,
})

function deferred(reply: MockReply) {
  let release!: () => void
  const gate = new Promise<void>((r) => (release = r))
  return { handler: async () => (await gate, reply), release: () => release() }
}

describe('navigation (4.1)', () => {
  it('shows only shipped sections: Projects in the app, Overview, Endpoints and Settings in a project', async () => {
    mockApi({ ...signedIn, [`GET /api/v1/projects/${ID}`]: { body: project() } })
    renderApp(`/projects/${ID}`)
    const main = await screen.findByRole('navigation', { name: 'Main' })
    expect(
      within(main)
        .getAllByRole('link')
        .map((l) => l.textContent),
    ).toEqual(['Projects'])

    const sub = await screen.findByRole('navigation', { name: 'Project' })
    expect(
      within(sub)
        .getAllByRole('link')
        .map((l) => l.textContent),
    ).toEqual(['Overview', 'Endpoints', 'Settings'])
    for (const missing of [/events/i, /replays/i]) {
      expect(within(sub).queryByRole('link', { name: missing })).not.toBeInTheDocument()
    }
    expect(within(sub).getByRole('link', { name: 'Overview' })).toHaveAttribute('aria-current', 'page')
  })
})

describe('project list (4.2)', () => {
  it('shows a loading state, then the projects', async () => {
    const list = deferred({ body: { items: [project()] } })
    mockApi({ ...signedIn, 'GET /api/v1/projects': list.handler })
    renderApp('/projects')
    expect(await screen.findByText('Loading projects…')).toBeInTheDocument()
    list.release()
    expect(await screen.findByRole('link', { name: /Stripe sandbox/ })).toHaveAttribute(
      'href',
      `/projects/${ID}`,
    )
    expect(screen.getByText('Payment provider test hooks')).toBeInTheDocument()
  })

  it('shows an honest empty state with a create action', async () => {
    mockApi(signedIn)
    renderApp('/projects')
    expect(await screen.findByRole('heading', { name: 'Create your first project' })).toBeInTheDocument()
    expect(screen.getByText(/groups the webhook endpoints/)).toBeInTheDocument()
    expect(screen.queryByRole('list', { name: 'Your projects' })).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Create project' })).toBeInTheDocument()
  })

  it('shows an error state and retries', async () => {
    let fail = true
    mockApi({
      ...signedIn,
      'GET /api/v1/projects': () =>
        fail ? apiError(500, 'internal_error') : { body: { items: [project()] } },
    })
    renderApp('/projects')
    expect(await screen.findByText('Your projects could not be loaded.')).toBeInTheDocument()
    fail = false
    await userEvent.click(screen.getByRole('button', { name: 'Try again' }))
    expect(await screen.findByRole('link', { name: /Stripe sandbox/ })).toBeInTheDocument()
  })

  it('renders HTML in names as literal text', async () => {
    const name = '<img src=x onerror=alert(1)>'
    mockApi({ ...signedIn, 'GET /api/v1/projects': { body: { items: [project({ name })] } } })
    const { container } = renderApp('/projects')
    expect(await screen.findByText(name)).toBeInTheDocument()
    expect(container.querySelector('img[src="x"]')).toBeNull()
  })

  it('creates a project: dialog closes, the list updates without a refetch, focus moves to the new project', async () => {
    let listCalls = 0
    const { calls } = mockApi({
      ...signedIn,
      'GET /api/v1/projects': () => {
        listCalls++
        return { body: { items: listCalls === 1 ? [] : [project()] } }
      },
      'POST /api/v1/projects': { status: 201, body: project() },
    })
    renderApp('/projects')
    await userEvent.click(await screen.findByRole('button', { name: 'Create project' }))
    const dialog = await screen.findByRole('dialog', { name: 'Create project' })
    const name = within(dialog).getByLabelText('Project name')
    expect(name).toHaveFocus()

    await userEvent.type(name, '  Stripe sandbox ')
    await userEvent.type(
      within(dialog).getByLabelText('Description (optional)'),
      'Payment provider test hooks',
    )
    await userEvent.click(within(dialog).getByRole('button', { name: 'Create project' }))

    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
    const link = await screen.findByRole('link', { name: /Stripe sandbox/ })
    await waitFor(() => expect(link).toHaveFocus())
    expect(calls.find((c) => c.method === 'POST')?.body).toEqual({
      name: 'Stripe sandbox',
      description: 'Payment provider test hooks',
    })
  })

  it('validates in the dialog and shows server field errors and the limit', async () => {
    let reply: MockReply = {
      status: 422,
      body: {
        error: {
          code: 'validation_failed',
          message: 'x',
          request_id: 'r',
          fields: { name: 'Use at most 80 characters.' },
        },
      },
    }
    mockApi({ ...signedIn, 'POST /api/v1/projects': () => reply })
    renderApp('/projects')
    await userEvent.click(await screen.findByRole('button', { name: 'Create project' }))
    const dialog = await screen.findByRole('dialog')
    await userEvent.click(within(dialog).getByRole('button', { name: 'Create project' }))
    expect(within(dialog).getByLabelText('Project name')).toHaveAccessibleDescription('Enter a project name.')

    await userEvent.type(within(dialog).getByLabelText('Project name'), 'x')
    await userEvent.click(within(dialog).getByRole('button', { name: 'Create project' }))
    await waitFor(() =>
      expect(within(dialog).getByLabelText('Project name')).toHaveAccessibleDescription(
        'Use at most 80 characters.',
      ),
    )

    reply = apiError(409, 'project_limit_reached')
    await userEvent.click(within(dialog).getByRole('button', { name: 'Create project' }))
    expect(await within(dialog).findByRole('alert')).toHaveTextContent('maximum number of projects')
  })

  it('closes the dialog with Escape and returns focus to the trigger', async () => {
    mockApi(signedIn)
    renderApp('/projects')
    const trigger = await screen.findByRole('button', { name: 'Create project' })
    await userEvent.click(trigger)
    await screen.findByRole('dialog')
    await userEvent.keyboard('{Escape}')
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    expect(trigger).toHaveFocus()
  })
})

describe('project overview (4.3)', () => {
  it('shows an owned project with setup guidance and honest empty sections', async () => {
    mockApi({ ...signedIn, [`GET /api/v1/projects/${ID}`]: { body: project() } })
    renderApp(`/projects/${ID}`)
    expect(await screen.findByRole('heading', { level: 1, name: 'Stripe sandbox' })).toBeInTheDocument()
    expect(screen.getByRole('heading', { name: 'Set up this project' })).toBeInTheDocument()
    expect(screen.getByText(/not available in this version/)).toBeInTheDocument()
    expect(screen.getByText('Payment provider test hooks')).toBeInTheDocument()
  })

  it.each([
    ['another user’s project', ID],
    ['a malformed id', 'not-a-uuid'],
  ])('shows "Project not found" for %s', async (_label, id) => {
    mockApi({ ...signedIn, [`GET /api/v1/projects/${id}`]: apiError(404, 'not_found') })
    renderApp(`/projects/${id}`)
    expect(await screen.findByRole('heading', { name: 'Project not found' })).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Back to projects' })).toHaveAttribute('href', '/projects')
  })
})

describe('project settings (4.4)', () => {
  it('renames the project and shows success', async () => {
    let current = project()
    const { calls } = mockApi({
      ...signedIn,
      [`GET /api/v1/projects/${ID}`]: () => ({ body: current }),
      [`PATCH /api/v1/projects/${ID}`]: (req) => {
        current = { ...current, ...(req.body as Partial<Project>) }
        return { body: current }
      },
    })
    renderApp(`/projects/${ID}/settings`)
    const details = await screen.findByRole('region', { name: 'Project details' })
    const name = within(details).getByLabelText('Project name')
    await waitFor(() => expect(name).toHaveValue('Stripe sandbox'))
    await userEvent.clear(name)
    await userEvent.type(name, 'GitHub app')
    await userEvent.clear(within(details).getByLabelText('Description (optional)'))
    await userEvent.click(within(details).getByRole('button', { name: 'Save changes' }))

    expect(await within(details).findByText('Project details saved.')).toBeInTheDocument()
    expect(calls.find((c) => c.method === 'PATCH')?.body).toEqual({ name: 'GitHub app', description: null })
    expect(await screen.findByRole('heading', { level: 1, name: 'GitHub app' })).toBeInTheDocument()
  })

  it('keeps delete disabled until the exact project name is typed, then deletes and returns to the list', async () => {
    const { calls } = mockApi({
      ...signedIn,
      [`GET /api/v1/projects/${ID}`]: { body: project() },
      [`DELETE /api/v1/projects/${ID}`]: { status: 204 },
    })
    const { router } = renderApp(`/projects/${ID}/settings`)
    const danger = await screen.findByRole('region', { name: 'Delete project' })
    const button = within(danger).getByRole('button', { name: 'Delete this project' })
    const confirm = within(danger).getByLabelText(/to confirm/)
    expect(button).toBeDisabled()

    await userEvent.type(confirm, 'stripe sandbox')
    expect(button).toBeDisabled()
    await userEvent.clear(confirm)
    await userEvent.type(confirm, 'Stripe sandbox')
    expect(button).toBeEnabled()

    await userEvent.click(button)
    await waitFor(() => expect(currentPath(router)).toBe('/projects'))
    expect(calls.some((c) => c.method === 'DELETE' && c.path === `/api/v1/projects/${ID}`)).toBe(true)
  })

  it('shows an error when deletion fails and stays on the page', async () => {
    mockApi({
      ...signedIn,
      [`GET /api/v1/projects/${ID}`]: { body: project() },
      [`DELETE /api/v1/projects/${ID}`]: apiError(500, 'internal_error'),
    })
    const { router } = renderApp(`/projects/${ID}/settings`)
    const danger = await screen.findByRole('region', { name: 'Delete project' })
    await userEvent.type(within(danger).getByLabelText(/to confirm/), 'Stripe sandbox')
    await userEvent.click(within(danger).getByRole('button', { name: 'Delete this project' }))
    expect(await within(danger).findByRole('alert')).toHaveTextContent('could not be deleted')
    expect(currentPath(router)).toBe(`/projects/${ID}/settings`)
  })
})
