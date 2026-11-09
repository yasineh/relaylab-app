import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'

import type { Endpoint } from '@/features/endpoints/api'
import type { Project } from '@/features/projects/api'
import { apiError, mockApi, signedIn, type Handler, type MockReply } from '@/test/api'
import { currentPath, renderApp } from '@/test/render'

const PID = '01936f3e-7b8a-7c1d-9e2f-0a1b2c3d4e5f'
const EID = '01936f3e-8c9d-7e1f-a2b3-c4d5e6f70819'
const URL1 = 'https://relaylab.test/h/AbCdEfGhIjKlMnOpQrStUvWxYz0123456789abcdefg'
const URL2 = 'https://relaylab.test/h/ZyXwVuTsRqPoNmLkJiHgFeDcBa9876543210zyxwvut'

const project: Project = {
  id: PID,
  name: 'Stripe sandbox',
  description: null,
  created_at: '2026-11-01T10:00:00Z',
  updated_at: '2026-11-01T10:00:00Z',
}

const endpoint = (over: Partial<Endpoint> = {}): Endpoint => ({
  id: EID,
  project_id: PID,
  name: 'Stripe staging',
  enabled: true,
  url: URL1,
  last_received_at: null,
  created_at: '2026-11-02T10:00:00Z',
  updated_at: '2026-11-02T10:00:00Z',
  ...over,
})

const LIST = `GET /api/v1/projects/${PID}/endpoints`
const ONE = `/api/v1/projects/${PID}/endpoints/${EID}`

function withProject(handlers: Record<string, Handler>) {
  return mockApi({ ...signedIn, [`GET /api/v1/projects/${PID}`]: { body: project }, ...handlers })
}

/** The URL shown in the "Receiving URL" section (the code block, not the curl example). */
const shownUrl = () =>
  within(screen.getByRole('region', { name: 'Receiving URL' })).getByText(/^https:/, { selector: 'code' })

function deferred(reply: MockReply) {
  let release!: () => void
  const gate = new Promise<void>((r) => (release = r))
  return { handler: async () => (await gate, reply), release: () => release() }
}

describe('endpoints list (4.1)', () => {
  it('shows a loading state, then endpoints newest first with status and last-received time', async () => {
    const list = deferred({
      body: {
        items: [
          endpoint(),
          endpoint({
            id: 'e2',
            name: 'GitHub app',
            enabled: false,
            last_received_at: '2026-11-03T09:00:00Z',
          }),
        ],
      },
    })
    withProject({ [LIST]: list.handler })
    renderApp(`/projects/${PID}/endpoints`)
    expect(await screen.findByText('Loading endpoints…')).toBeInTheDocument()
    list.release()

    const table = await screen.findByRole('table')
    const rows = within(table).getAllByRole('row').slice(1)
    expect(rows.map((r) => within(r).getAllByRole('rowheader')[0]!.textContent)).toEqual([
      'Stripe staging',
      'GitHub app',
    ])
    expect(within(rows[0]!).getByText('Enabled')).toBeInTheDocument()
    expect(within(rows[0]!).getByText('Never')).toBeInTheDocument()
    expect(within(rows[1]!).getByText('Disabled')).toBeInTheDocument()
    expect(within(rows[0]!).getByRole('link', { name: 'Stripe staging' })).toHaveAttribute(
      'href',
      `/projects/${PID}/endpoints/${EID}`,
    )
    // The receiving URL is a secret: it is shown on the detail page only.
    expect(screen.queryByText(URL1)).not.toBeInTheDocument()
    expect(
      within(screen.getByRole('navigation', { name: 'Project' })).getByRole('link', { name: 'Endpoints' }),
    ).toHaveAttribute('aria-current', 'page')
  })

  it('shows an empty state with a create action', async () => {
    withProject({ [LIST]: { body: { items: [] } } })
    renderApp(`/projects/${PID}/endpoints`)
    expect(await screen.findByRole('heading', { name: 'Create your first endpoint' })).toBeInTheDocument()
    expect(screen.queryByRole('table')).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Create endpoint' })).toBeInTheDocument()
  })

  it('shows an error state and retries', async () => {
    let fail = true
    withProject({
      [LIST]: () => (fail ? apiError(500, 'internal_error') : { body: { items: [endpoint()] } }),
    })
    renderApp(`/projects/${PID}/endpoints`)
    expect(await screen.findByText('The endpoints could not be loaded.')).toBeInTheDocument()
    fail = false
    await userEvent.click(screen.getByRole('button', { name: 'Try again' }))
    expect(await screen.findByRole('link', { name: 'Stripe staging' })).toBeInTheDocument()
  })

  it('renders HTML in names as literal text', async () => {
    const name = '<img src=x onerror=alert(1)>'
    withProject({ [LIST]: { body: { items: [endpoint({ name })] } } })
    const { container } = renderApp(`/projects/${PID}/endpoints`)
    expect(await screen.findByText(name)).toBeInTheDocument()
    expect(container.querySelector('img[src="x"]')).toBeNull()
  })
})

describe('create and rename dialogs (4.2)', () => {
  it('creates an endpoint with a trimmed name and opens its detail page', async () => {
    const { calls } = withProject({
      [LIST]: { body: { items: [] } },
      [`POST /api/v1/projects/${PID}/endpoints`]: { status: 201, body: endpoint() },
      [`GET ${ONE}`]: { body: endpoint() },
    })
    const { router } = renderApp(`/projects/${PID}/endpoints`)
    await userEvent.click(await screen.findByRole('button', { name: 'Create endpoint' }))
    const dialog = await screen.findByRole('dialog', { name: 'Create endpoint' })
    const name = within(dialog).getByLabelText('Endpoint name')
    expect(name).toHaveFocus()
    await userEvent.type(name, '  Stripe staging ')
    await userEvent.click(within(dialog).getByRole('button', { name: 'Create endpoint' }))

    await waitFor(() => expect(currentPath(router)).toBe(`/projects/${PID}/endpoints/${EID}`))
    expect(calls.find((c) => c.method === 'POST')?.body).toEqual({ name: 'Stripe staging' })
    expect(await screen.findByRole('region', { name: 'Receiving URL' })).toBeInTheDocument()
    expect(shownUrl()).toHaveTextContent(URL1)
  })

  it('validates like the server and shows server field errors and the limit', async () => {
    let reply: MockReply = {
      status: 422,
      body: {
        error: {
          code: 'validation_failed',
          message: 'x',
          request_id: 'r',
          fields: { name: 'Server says no.' },
        },
      },
    }
    const { calls } = withProject({
      [LIST]: { body: { items: [] } },
      [`POST /api/v1/projects/${PID}/endpoints`]: () => reply,
    })
    renderApp(`/projects/${PID}/endpoints`)
    await userEvent.click(await screen.findByRole('button', { name: 'Create endpoint' }))
    const dialog = await screen.findByRole('dialog')
    const name = within(dialog).getByLabelText('Endpoint name')
    const submit = within(dialog).getByRole('button', { name: 'Create endpoint' })

    await userEvent.type(name, '   ')
    await userEvent.click(submit)
    expect(name).toHaveAccessibleDescription('Enter an endpoint name.')

    await userEvent.clear(name)
    await userEvent.type(name, 'x'.repeat(101))
    await userEvent.click(submit)
    expect(name).toHaveAccessibleDescription('Use at most 100 characters.')
    expect(calls.some((c) => c.method === 'POST')).toBe(false)

    await userEvent.clear(name)
    await userEvent.type(name, 'Valid name')
    await userEvent.click(submit)
    await waitFor(() => expect(name).toHaveAccessibleDescription('Server says no.'))

    reply = apiError(409, 'endpoint_limit_reached')
    await userEvent.click(submit)
    expect(await within(dialog).findByRole('alert')).toHaveTextContent('reached its endpoint limit')
  })

  it('rejects control characters before calling the API', async () => {
    const { calls } = withProject({ [LIST]: { body: { items: [] } } })
    renderApp(`/projects/${PID}/endpoints`)
    await userEvent.click(await screen.findByRole('button', { name: 'Create endpoint' }))
    const dialog = await screen.findByRole('dialog')
    const name = within(dialog).getByLabelText('Endpoint name')
    await userEvent.type(name, 'bad\u0007name')
    await userEvent.click(within(dialog).getByRole('button', { name: 'Create endpoint' }))
    expect(name).toHaveAccessibleDescription('Name must not contain control characters.')
    expect(calls.some((c) => c.method === 'POST')).toBe(false)
  })

  it('renames an endpoint; the URL stays the same', async () => {
    let current = endpoint()
    const { calls } = withProject({
      [`GET ${ONE}`]: () => ({ body: current }),
      [`PATCH ${ONE}`]: (req) => {
        current = { ...current, ...(req.body as Partial<Endpoint>) }
        return { body: current }
      },
    })
    renderApp(`/projects/${PID}/endpoints/${EID}`)
    await userEvent.click(await screen.findByRole('button', { name: 'Rename' }))
    const dialog = await screen.findByRole('dialog', { name: 'Rename endpoint' })
    const name = within(dialog).getByLabelText('Endpoint name')
    expect(name).toHaveValue('Stripe staging')
    await userEvent.clear(name)
    await userEvent.type(name, 'Stripe live')
    await userEvent.click(within(dialog).getByRole('button', { name: 'Save name' }))

    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
    expect(await screen.findByRole('heading', { name: 'Stripe live' })).toBeInTheDocument()
    expect(shownUrl()).toHaveTextContent(URL1)
    expect(calls.find((c) => c.method === 'PATCH')?.body).toEqual({ name: 'Stripe live' })
  })
})

describe('endpoint detail (4.3)', () => {
  it('copies the exact URL and the curl example, with an announced confirmation', async () => {
    const user = userEvent.setup()
    withProject({ [`GET ${ONE}`]: { body: endpoint() } })
    renderApp(`/projects/${PID}/endpoints/${EID}`)

    const curl = `curl -X POST '${URL1}' -H 'Content-Type: application/json' -d '{"hello":"relaylab"}'`
    expect(await screen.findByText(curl, { selector: 'pre' })).toBeInTheDocument()

    // Explains the acknowledgement and redaction, and is honest that the event explorer has not shipped.
    expect(screen.getByText(/replaced with/)).toHaveTextContent('Authorization and Cookie')
    expect(screen.getByText(/Viewing received requests is not available/)).toBeInTheDocument()
    expect(screen.queryByText(/Receiving requests is not available/)).not.toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: 'Copy URL' }))
    expect(await navigator.clipboard.readText()).toBe(URL1)
    expect(await screen.findByText('Copied')).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: 'Copy curl command' }))
    expect(await navigator.clipboard.readText()).toBe(curl)
  })

  it('toggles the enabled switch through the API', async () => {
    let current = endpoint()
    const { calls } = withProject({
      [`GET ${ONE}`]: () => ({ body: current }),
      [`PATCH ${ONE}`]: (req) => {
        current = { ...current, ...(req.body as Partial<Endpoint>) }
        return { body: current }
      },
    })
    renderApp(`/projects/${PID}/endpoints/${EID}`)
    const toggle = await screen.findByRole('switch', { name: 'Accept requests' })
    expect(toggle).toBeChecked()

    await userEvent.click(toggle)
    await waitFor(() => expect(toggle).not.toBeChecked())
    expect(screen.getAllByText('Disabled').length).toBeGreaterThan(0)
    expect(screen.getByText(/requests to this URL are rejected/)).toBeInTheDocument()
    expect(calls.filter((c) => c.method === 'PATCH').map((c) => c.body)).toEqual([{ enabled: false }])

    await userEvent.click(toggle)
    await waitFor(() => expect(toggle).toBeChecked())
  })

  it('shows "Endpoint not found" for an unknown or foreign endpoint', async () => {
    withProject({ [`GET ${ONE}`]: apiError(404, 'not_found') })
    renderApp(`/projects/${PID}/endpoints/${EID}`)
    expect(await screen.findByRole('heading', { name: 'Endpoint not found' })).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Back to endpoints' })).toHaveAttribute(
      'href',
      `/projects/${PID}/endpoints`,
    )
  })

  it('explains a missing URL and points to rotation', async () => {
    withProject({ [`GET ${ONE}`]: { body: endpoint({ url: null }) } })
    renderApp(`/projects/${PID}/endpoints/${EID}`)
    expect(await screen.findByText(/receiving URL cannot be shown/)).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Copy URL' })).not.toBeInTheDocument()
  })
})

describe('rotate and delete dialogs (4.4)', () => {
  it('rotation: cancel changes nothing; confirm calls the API and shows the new URL', async () => {
    let current = endpoint()
    const { calls } = withProject({
      [`GET ${ONE}`]: () => ({ body: current }),
      [`POST ${ONE}/rotate`]: () => {
        current = { ...current, url: URL2 }
        return { body: current }
      },
    })
    renderApp(`/projects/${PID}/endpoints/${EID}`)
    const trigger = await screen.findByRole('button', { name: 'Rotate URL' })

    await userEvent.click(trigger)
    let dialog = await screen.findByRole('dialog', { name: 'Rotate receiving URL?' })
    expect(dialog).toHaveAccessibleDescription(/stops working immediately/)
    // Focus starts on Cancel, so pressing Enter by accident does not rotate.
    expect(within(dialog).getByRole('button', { name: 'Cancel' })).toHaveFocus()
    await userEvent.keyboard('{Escape}')
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    expect(trigger).toHaveFocus()
    expect(calls.some((c) => c.path.endsWith('/rotate'))).toBe(false)
    expect(shownUrl()).toHaveTextContent(URL1)

    await userEvent.click(trigger)
    dialog = await screen.findByRole('dialog')
    await userEvent.click(within(dialog).getByRole('button', { name: 'Rotate URL' }))
    await waitFor(() => expect(shownUrl()).toHaveTextContent(URL2))
    expect(screen.getByText(/New URL issued/)).toBeInTheDocument()
    expect(calls.filter((c) => c.method === 'POST' && c.path.endsWith('/rotate'))).toHaveLength(1)
  })

  it('rotation failure keeps the dialog open with an error', async () => {
    withProject({
      [`GET ${ONE}`]: { body: endpoint() },
      [`POST ${ONE}/rotate`]: apiError(500, 'internal_error'),
    })
    renderApp(`/projects/${PID}/endpoints/${EID}`)
    await userEvent.click(await screen.findByRole('button', { name: 'Rotate URL' }))
    const dialog = await screen.findByRole('dialog')
    await userEvent.click(within(dialog).getByRole('button', { name: 'Rotate URL' }))
    expect(await within(dialog).findByRole('alert')).toHaveTextContent('current URL still works')
    expect(shownUrl()).toHaveTextContent(URL1)
  })

  it('delete: cancel leaves it unchanged; the exact typed name enables delete, which returns to the list', async () => {
    const { calls } = withProject({
      [`GET ${ONE}`]: { body: endpoint() },
      [LIST]: { body: { items: [] } },
      [`DELETE ${ONE}`]: { status: 204 },
    })
    const { router } = renderApp(`/projects/${PID}/endpoints/${EID}`)
    const trigger = await screen.findByRole('button', { name: 'Delete endpoint' })

    await userEvent.click(trigger)
    let dialog = await screen.findByRole('dialog', { name: 'Delete endpoint?' })
    const typed = within(dialog).getByLabelText(/to confirm/)
    expect(typed).toHaveFocus()
    await userEvent.type(typed, 'Stripe staging')
    await userEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }))
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    expect(calls.some((c) => c.method === 'DELETE')).toBe(false)

    // Reopening starts empty: the earlier typed confirmation does not carry over.
    await userEvent.click(trigger)
    dialog = await screen.findByRole('dialog')
    const confirm = within(dialog).getByRole('button', { name: 'Delete endpoint' })
    expect(within(dialog).getByLabelText(/to confirm/)).toHaveValue('')
    expect(confirm).toBeDisabled()
    await userEvent.type(within(dialog).getByLabelText(/to confirm/), 'stripe staging')
    expect(confirm).toBeDisabled()
    await userEvent.clear(within(dialog).getByLabelText(/to confirm/))
    // Keyboard only: type the name, then submit with Enter.
    await userEvent.type(within(dialog).getByLabelText(/to confirm/), 'Stripe staging{Enter}')

    await waitFor(() => expect(currentPath(router)).toBe(`/projects/${PID}/endpoints`))
    expect(calls.some((c) => c.method === 'DELETE' && c.path === ONE)).toBe(true)
  })

  it('delete failure keeps the dialog open with an error', async () => {
    withProject({ [`GET ${ONE}`]: { body: endpoint() }, [`DELETE ${ONE}`]: apiError(500, 'internal_error') })
    const { router } = renderApp(`/projects/${PID}/endpoints/${EID}`)
    await userEvent.click(await screen.findByRole('button', { name: 'Delete endpoint' }))
    const dialog = await screen.findByRole('dialog')
    await userEvent.type(within(dialog).getByLabelText(/to confirm/), 'Stripe staging')
    await userEvent.click(within(dialog).getByRole('button', { name: 'Delete endpoint' }))
    expect(await within(dialog).findByRole('alert')).toHaveTextContent('could not be deleted')
    expect(currentPath(router)).toBe(`/projects/${PID}/endpoints/${EID}`)
  })
})
