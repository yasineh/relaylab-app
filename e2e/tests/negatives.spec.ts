import { randomUUID } from 'node:crypto'

import {
  createEndpoint,
  createProject,
  expect,
  sendWebhook,
  signUp,
  test,
  type Endpoint,
  type Project,
} from '../fixtures/app.ts'
import { BASE_URL, LIMITS } from '../fixtures/env.ts'
import { expireSessions } from '../fixtures/db.ts'
import { serviceLogs } from '../fixtures/stack.ts'

test.describe('invalid payloads (3.1)', () => {
  test('malformed JSON is stored and shown as received, with a parse notice', async ({
    page,
    user: _u,
    request,
  }) => {
    const project = await createProject(page)
    const endpoint = await createEndpoint(page, project.id)
    const body = '{"order": 42, "items": [1, 2,'
    const res = await sendWebhook(request, endpoint.url, {
      path: '/broken',
      headers: { 'Content-Type': 'application/json' },
      body,
    })
    expect(res.status()).toBe(200)

    await page.goto(`/projects/${project.id}/events`)
    await page.getByRole('table', { name: 'Received events' }).getByRole('link', { name: '/broken' }).click()
    await page.getByRole('tab', { name: /^Body/ }).click()
    const panel = page.getByRole('tabpanel')
    await expect(
      panel.getByText('The content type says JSON, but the body is not valid JSON. It is shown as received.'),
    ).toBeVisible()
    await expect(panel).toContainText(body)
    await expect(page.getByRole('alert')).toHaveCount(0)
  })
})

test.describe('unauthorized access (3.2)', () => {
  let owner: { project: Project; endpoint: Endpoint; eventPath: string }

  test('another user sees not-found states and none of the owner’s data', async ({ browser, request }) => {
    const ownerPage = await (await browser.newContext()).newPage()
    await signUp(ownerPage)
    const project = await createProject(ownerPage, `Secret project ${randomUUID().slice(0, 6)}`)
    const endpoint = await createEndpoint(ownerPage, project.id, 'Secret endpoint')
    await sendWebhook(request, endpoint.url, { path: '/secret-path', body: 'secret-body-value' })
    const list = await ownerPage.request.get(`${BASE_URL}/api/v1/projects/${project.id}/events`)
    const eventId = ((await list.json()) as { items: { id: string }[] }).items[0]?.id
    expect(eventId).toBeTruthy()
    owner = { project, endpoint, eventPath: `/projects/${project.id}/events/${eventId}` }

    const intruder = await (await browser.newContext()).newPage()
    await signUp(intruder)
    for (const path of [
      `/projects/${owner.project.id}`,
      `/projects/${owner.project.id}/events`,
      owner.eventPath,
      `/projects/${owner.project.id}/endpoints/${owner.endpoint.id}`,
      `/projects/${owner.project.id}/replays`,
    ]) {
      await intruder.goto(path)
      await expect(intruder.getByRole('heading', { name: 'Project not found' })).toBeVisible()
      const text = await intruder.locator('body').innerText()
      for (const secret of [owner.project.name, 'Secret endpoint', '/secret-path', 'secret-body-value']) {
        expect(text, `${path} shows ${secret}`).not.toContain(secret)
      }
    }
    const api = await intruder.request.get(`${BASE_URL}/api/v1/projects/${owner.project.id}/events`)
    expect(api.status()).toBe(404)
  })

  test('a signed-out browser is sent to sign-in', async ({ page }) => {
    await page.goto('/projects')
    await expect(page).toHaveURL(/\/sign-in/)
    const api = await page.request.get(`${BASE_URL}/api/v1/projects`)
    expect(api.status()).toBe(401)
  })
})

test.describe('sessions (3.6)', () => {
  test('a tampered session cookie redirects to sign-in and gets 401 from the API', async ({
    page,
    user: _u,
    context,
  }) => {
    const project = await createProject(page)
    const cookies = await context.cookies(BASE_URL)
    const session = cookies.find((c) => c.name.endsWith('session_token'))
    expect(session, 'session cookie').toBeTruthy()
    await context.addCookies([{ ...session!, value: `${session!.value.slice(0, -4)}AAAA` }])

    await page.goto(`/projects/${project.id}`)
    await expect(page).toHaveURL(/\/sign-in/)
    await expect(page.getByText(project.name)).toHaveCount(0)
    expect((await page.request.get(`${BASE_URL}/api/v1/projects`)).status()).toBe(401)
  })

  test('an expired session shows the expired notice and no project data', async ({ page, user }) => {
    const project = await createProject(page)
    await page.goto('/projects')
    await expect(page.getByRole('link', { name: new RegExp(project.name) })).toBeVisible()

    expect(await expireSessions(user.email)).toBeGreaterThan(0)
    await page.getByRole('link', { name: new RegExp(project.name) }).click()
    await expect(page).toHaveURL(/\/sign-in\?.*reason=expired/)
    await expect(page.getByText('Your session has expired. Sign in again to continue.')).toBeVisible()
    await expect(page.getByText(project.name)).toHaveCount(0)
    expect((await page.request.get(`${BASE_URL}/api/v1/projects`)).status()).toBe(401)
  })
})

test.describe('ingestion limits', () => {
  test('an oversized request gets 413 and no event appears (3.3)', async ({ page, user: _u, request }) => {
    const project = await createProject(page)
    const endpoint = await createEndpoint(page, project.id)
    const res = await sendWebhook(request, endpoint.url, {
      headers: { 'Content-Type': 'text/plain' },
      body: 'x'.repeat(LIMITS.ingestMaxBodyBytes + 1),
    })
    expect(res.status()).toBe(413)
    await page.goto(`/projects/${project.id}/events`)
    await expect(page.getByRole('heading', { name: 'No events yet' })).toBeVisible()
  })

  test('requests beyond the endpoint rate limit get 429 with Retry-After (3.4)', async ({
    page,
    user: _u,
    request,
  }) => {
    const project = await createProject(page)
    const endpoint = await createEndpoint(page, project.id)
    const results = await Promise.all(
      Array.from({ length: LIMITS.endpointBurst + 5 }, () =>
        sendWebhook(request, endpoint.url, { body: 'x' }),
      ),
    )
    const ok = results.filter((r) => r.status() === 200)
    const limited = results.filter((r) => r.status() === 429)
    expect(ok.length + limited.length).toBe(results.length)
    // The bucket refills one token every 10 s, so at most one extra request fits.
    expect(limited.length).toBeGreaterThanOrEqual(4)
    expect(ok.length).toBeGreaterThanOrEqual(LIMITS.endpointBurst)
    for (const r of limited) {
      expect(Number(r.headers()['retry-after'])).toBeGreaterThan(0)
    }
  })
})

test.describe('unsafe replay destinations (3.5)', () => {
  test('loopback, private, metadata, and plain-HTTP destinations are blocked before anything is sent', async ({
    page,
    user: _u,
    request,
  }) => {
    const since = new Date().toISOString()
    const project = await createProject(page)
    const endpoint = await createEndpoint(page, project.id)
    await sendWebhook(request, endpoint.url, { path: '/to-replay', body: 'x' })
    await page.goto(`/projects/${project.id}/events`)
    await page
      .getByRole('table', { name: 'Received events' })
      .getByRole('link', { name: '/to-replay' })
      .click()

    const eventId = new URL(page.url()).pathname.split('/').pop()!
    for (const [url, message] of [
      ['https://169.254.169.254/latest/meta-data/', /non-public address/],
      ['https://127.0.0.1/', /non-public address/],
      ['https://10.0.0.5/', /non-public address/],
      ['http://example.com/', /Only https:\/\/ destinations are allowed/],
    ] as const) {
      // The dialog checks the destination with the server as it is typed and
      // keeps Send disabled.
      await page.getByRole('button', { name: 'Replay' }).click()
      const dialog = page.getByRole('dialog', { name: 'Replay event' })
      await dialog.getByRole('radio', { name: 'URL' }).check()
      await dialog.getByLabel('Destination URL').fill(url)
      await expect(dialog.getByText(message).first(), url).toBeVisible()
      await expect(dialog.getByRole('button', { name: 'Send replay' })).toBeDisabled()
      await dialog.getByRole('button', { name: 'Cancel' }).click()
      await expect(dialog).toBeHidden()

      // Creating it through the API directly is refused too.
      const res = await page.request.post(
        `${BASE_URL}/api/v1/projects/${project.id}/events/${eventId}/replays`,
        {
          headers: { Origin: BASE_URL, 'Idempotency-Key': randomUUID() },
          data: { destination: { type: 'url', url } },
        },
      )
      expect(res.status(), url).toBe(422)
    }
    await expect(page.getByText('Not replayed yet.', { exact: false })).toBeVisible()
    const replays = await page.request.get(`${BASE_URL}/api/v1/projects/${project.id}/replays`)
    expect(((await replays.json()) as { items: unknown[] }).items).toHaveLength(0)
    // Nothing was queued, so the worker never attempted these addresses. (The
    // offline test replays to example.com in parallel, so only the IP
    // destinations, unique to this test, are checked in the worker log.)
    const workerLog = serviceLogs('worker', since)
    for (const host of ['169.254.169.254', '127.0.0.1', '10.0.0.5']) {
      expect(workerLog).not.toContain(`"host":"${host}"`)
    }
  })
})
