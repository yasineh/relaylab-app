import { expect, test } from '@playwright/test'

import { sendWebhook, uniqueEmail, uniqueName } from '../fixtures/app.ts'
import { PASSWORD } from '../fixtures/env.ts'
import { verificationLink } from '../fixtures/mail.ts'

/**
 * The critical journey (claude.md M6.5): sign up with email verification,
 * create a project and endpoints, receive a webhook live, inspect it, and
 * replay it to the test sink and to the controlled echo destination.
 */
test('sign up → project → endpoint → live webhook → inspect → replay', async ({ page, request }) => {
  const email = uniqueEmail()
  const projectName = uniqueName('Payments')

  await test.step('sign up and verify the email through Mailpit', async () => {
    await page.goto('/sign-up')
    await page.getByLabel('Name').fill('E2E Journey')
    await page.getByLabel('Email').fill(email)
    await page.getByLabel('Password').fill(PASSWORD)
    await page.getByRole('button', { name: 'Create account' }).click()
    await expect(page).toHaveURL(/\/check-email/)
    await page.goto(await verificationLink(email))
    await expect(page.getByRole('heading', { name: 'Email verified' })).toBeVisible()
    await page.getByRole('link', { name: 'Continue to RelayLab' }).click()
    await expect(page).toHaveURL(/\/projects$/)
  })

  await test.step('create a project', async () => {
    await page.getByRole('button', { name: 'Create project' }).click()
    const dialog = page.getByRole('dialog', { name: 'Create project' })
    await dialog.getByLabel('Project name').fill(projectName)
    await dialog.getByRole('button', { name: 'Create project' }).click()
    await page.getByRole('link', { name: new RegExp(projectName) }).click()
    await expect(page.getByRole('heading', { level: 1, name: projectName })).toBeVisible()
  })

  const createEndpoint = async (name: string) => {
    await page.getByRole('navigation', { name: 'Project' }).getByRole('link', { name: 'Endpoints' }).click()
    await page.getByRole('button', { name: 'Create endpoint' }).first().click()
    const dialog = page.getByRole('dialog', { name: 'Create endpoint' })
    await dialog.getByLabel('Endpoint name').fill(name)
    await dialog.getByRole('button', { name: /Create/ }).click()
    await expect(page.getByRole('heading', { name })).toBeVisible()
    const url = await page.getByRole('region', { name: 'Receiving URL' }).locator('code').first().innerText()
    expect(url).toMatch(/^http:\/\/localhost:\d+\/h\//)
    return url
  }
  const url = await test.step('create the receiving endpoint', () => createEndpoint('Stripe'))
  await test.step('create a second endpoint to act as the test sink', () => createEndpoint('Sink'))

  await test.step('a webhook appears live, without a reload', async () => {
    await page.getByRole('navigation', { name: 'Project' }).getByRole('link', { name: 'Events' }).click()
    await expect(page.getByText('Live', { exact: true })).toBeVisible()
    await page.evaluate(() => ((window as unknown as { e2eMarker: boolean }).e2eMarker = true))
    const res = await sendWebhook(request, url, {
      path: '/charges',
      query: 'attempt=2&mode=test',
      headers: { 'Content-Type': 'application/json', 'X-Request-Source': 'e2e' },
      body: JSON.stringify({ id: 'evt_e2e_1', amount: 4200, currency: 'eur' }),
    })
    expect(res.status()).toBe(200)
    const table = page.getByRole('table', { name: 'Received events' })
    await expect(table.getByRole('row').filter({ hasText: '/charges' })).toBeVisible({ timeout: 5_000 })
    // The marker survives only if the page was never reloaded.
    expect(await page.evaluate(() => (window as unknown as { e2eMarker?: boolean }).e2eMarker)).toBe(true)
  })

  await test.step('inspect method, headers, query, and body', async () => {
    await page.getByRole('table', { name: 'Received events' }).getByRole('link', { name: '/charges' }).click()
    await expect(page.getByRole('heading', { name: '/charges?attempt=2&mode=test' })).toBeVisible()
    await expect(page.getByText('POST', { exact: true }).first()).toBeVisible()

    await page.getByRole('tab', { name: /^Headers/ }).click()
    await expect(
      page.getByRole('tabpanel').getByRole('row', { name: /X-Request-Source\s+e2e/i }),
    ).toBeVisible()

    await page.getByRole('tab', { name: /^Query/ }).click()
    const query = page.getByRole('table', { name: 'Query parameters' })
    await expect(query.getByRole('row', { name: 'attempt 2' })).toBeVisible()
    await expect(query.getByRole('row', { name: 'mode test' })).toBeVisible()

    await page.getByRole('tab', { name: /^Body/ }).click()
    await expect(page.getByRole('tabpanel').getByText('evt_e2e_1')).toBeVisible()
    await expect(page.getByRole('tabpanel').getByText('4200')).toBeVisible()
  })

  await test.step('replay to the test sink', async () => {
    await page.getByRole('button', { name: 'Replay' }).click()
    const dialog = page.getByRole('dialog', { name: 'Replay event' })
    await dialog.getByRole('radio', { name: 'Test sink' }).check()
    await expect(dialog.getByLabel('Sink endpoint')).toHaveValue(/.+/)
    await dialog.getByRole('button', { name: 'Send replay' }).click()
    await expect(dialog).toBeHidden()
    const sinkReplay = page
      .getByRole('list', { name: 'Replays of this event' })
      .getByRole('listitem')
      .filter({ hasText: 'Test sink: Sink' })
    await expect(sinkReplay).toContainText('Succeeded', { timeout: 15_000 })
  })

  await test.step('replay to the controlled echo destination', async () => {
    await page.getByRole('button', { name: 'Replay' }).click()
    const dialog = page.getByRole('dialog', { name: 'Replay event' })
    await dialog.getByRole('radio', { name: 'URL' }).check()
    await dialog.getByLabel('Destination URL').fill('http://echo:8080/replayed')
    await dialog.getByRole('button', { name: 'Send replay' }).click()
    await expect(dialog).toBeHidden()
    const echoReplay = page
      .getByRole('list', { name: 'Replays of this event' })
      .getByRole('listitem')
      .filter({ hasText: 'http://echo:8080/replayed' })
    await expect(echoReplay).toContainText('Succeeded · 200', { timeout: 15_000 })
  })

  await test.step('replay history shows the result and the response', async () => {
    await page
      .getByRole('list', { name: 'Replays of this event' })
      .getByRole('listitem')
      .filter({ hasText: 'http://echo:8080/replayed' })
      .getByRole('link', { name: /Details of replay/ })
      .click()
    await expect(page).toHaveURL(/\/replays\?replay=/)
    await expect(page.getByRole('heading', { name: 'Response 200' })).toBeVisible()
    await expect(page.getByLabel('Response body')).toContainText('"echo":true')
    await expect(page.getByLabel('Response body')).toContainText('"path":"/replayed"')
  })
})
