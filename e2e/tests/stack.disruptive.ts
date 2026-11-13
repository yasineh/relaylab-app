import {
  createEndpoint,
  createProject,
  expect,
  sendWebhook,
  test,
  uniqueEmail,
  viaHost,
} from '../fixtures/app.ts'
import { userExists } from '../fixtures/db.ts'
import { BASE_URL, INGEST_URL, PASSWORD } from '../fixtures/env.ts'
import { compose, setSignupEnabled } from '../fixtures/stack.ts'

/**
 * Tests that stop, restart, or reconfigure stack services. They run in the
 * `disruptive` Playwright project, after every other test, one at a time
 * (serial mode: two of them must never overlap).
 */
test.describe.configure({ mode: 'serial' })

/**
 * Restarts the api the browser is connected to, so it runs in its own
 * Playwright project after every other test (see playwright.config.ts).
 * Webhooks sent during the outage go to the second, ingest-only api on the
 * same database; the page recovers them when it reconnects.
 */
test('events received while the api restarts appear after reconnecting, without a reload', async ({
  page,
  user: _user,
  request,
}) => {
  const project = await createProject(page)
  const endpoint = await createEndpoint(page, project.id)
  await page.goto(`/projects/${project.id}/events`)
  await expect(page.getByText('Live', { exact: true })).toBeVisible()
  expect((await sendWebhook(request, endpoint.url, { path: '/before' })).status()).toBe(200)
  const table = page.getByRole('table', { name: 'Received events' })
  await expect(table.getByRole('link', { name: '/before' })).toBeVisible({ timeout: 5_000 })
  await page.evaluate(() => ((window as unknown as { e2eMarker: boolean }).e2eMarker = true))

  compose('stop', 'api')
  try {
    await expect(page.getByText(/Reconnecting…|Offline/)).toBeVisible({ timeout: 15_000 })
    for (const path of ['/during-1', '/during-2']) {
      const res = await sendWebhook(request, viaHost(endpoint.url, INGEST_URL), { path })
      expect(res.status()).toBe(200)
    }
  } finally {
    compose('start', 'api')
  }

  await expect(page.getByText('Live', { exact: true })).toBeVisible({ timeout: 30_000 })
  for (const path of ['/during-1', '/during-2']) {
    await expect(table.getByRole('link', { name: path })).toBeVisible({ timeout: 10_000 })
  }
  await expect(table.getByRole('link', { name: '/before' })).toHaveCount(1)
  expect(await page.evaluate(() => (window as unknown as { e2eMarker?: boolean }).e2eMarker)).toBe(true)
})

/**
 * Restarts the auth service and the api with AUTH_SIGNUP_ENABLED=false, so
 * it runs in the disruptive project, after everything else, and restores
 * sign-up afterwards.
 */
test.describe('sign-up disabled (3.7)', () => {
  test.afterAll(() => setSignupEnabled(true))

  test('no account is created and the UI says sign-up is unavailable', async ({ page, browser }) => {
    // A visitor opened the form while sign-up was still open.
    await page.goto('/sign-up')
    const email = uniqueEmail()
    await page.getByLabel('Name').fill('Late Visitor')
    await page.getByLabel('Email').fill(email)
    await page.getByLabel('Password').fill(PASSWORD)

    setSignupEnabled(false)

    await page.getByRole('button', { name: 'Create account' }).click()
    await expect(page.getByRole('alert')).toContainText('Sign-up is not available.')
    await expect(page).toHaveURL(/\/sign-up/)
    expect(await userExists(email)).toBe(false)

    // Directly against the auth API: refused, still no account.
    const direct = await page.request.post(`${BASE_URL}/api/auth/sign-up/email`, {
      headers: { Origin: BASE_URL },
      data: { email, password: PASSWORD, name: 'Direct', callbackURL: '/verify-email' },
      failOnStatusCode: false,
    })
    expect(direct.ok()).toBe(false)
    expect(await userExists(email)).toBe(false)

    // A new visitor: no sign-up page and no link to one.
    const fresh = await (await browser.newContext()).newPage()
    await fresh.goto('/sign-in')
    await expect(fresh.getByRole('heading', { name: 'Sign in to RelayLab' })).toBeVisible()
    await expect(fresh.getByRole('link', { name: 'Create an account' })).toHaveCount(0)
    await fresh.goto('/sign-up')
    await expect(fresh.getByRole('heading', { name: 'Page not found' })).toBeVisible()
    await expect(fresh.getByRole('button', { name: 'Create account' })).toHaveCount(0)
  })
})
