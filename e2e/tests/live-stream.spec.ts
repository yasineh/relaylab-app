import { createEndpoint, createProject, expect, sendWebhook, test } from '../fixtures/app.ts'

/**
 * Live stream behaviour in a real browser (moved here from
 * add-live-event-stream task 4.1).
 */
test('signing out in another tab ends the stream and redirects with the expired notice', async ({
  page,
  user: _user,
  context,
}) => {
  const project = await createProject(page)
  await page.goto(`/projects/${project.id}/events`)
  await expect(page.getByText('Live', { exact: true })).toBeVisible()

  const other = await context.newPage()
  await other.goto('/projects')
  await other.getByRole('button', { name: 'Sign out' }).click()
  await expect(other).toHaveURL(/\/sign-in/)

  // The stream re-checks the session every STREAM_REVALIDATE_INTERVAL (2 s in e2e).
  await expect(page).toHaveURL(/\/sign-in\?.*reason=expired/, { timeout: 15_000 })
  await expect(page.getByText('Your session has expired. Sign in again to continue.')).toBeVisible()
  await expect(page.getByText(project.name)).toHaveCount(0)
})

test('a webhook sent while the open page is connected shows up live', async ({
  page,
  user: _user,
  request,
}) => {
  const project = await createProject(page)
  const endpoint = await createEndpoint(page, project.id)
  await page.goto(`/projects/${project.id}/events`)
  await expect(page.getByText('Live', { exact: true })).toBeVisible()
  await page.evaluate(() => ((window as unknown as { e2eMarker: boolean }).e2eMarker = true))

  for (const path of ['/one', '/two', '/three']) {
    expect((await sendWebhook(request, endpoint.url, { path })).status()).toBe(200)
  }
  const table = page.getByRole('table', { name: 'Received events' })
  for (const path of ['/one', '/two', '/three']) {
    await expect(table.getByRole('link', { name: path })).toBeVisible({ timeout: 5_000 })
  }
  expect(await page.evaluate(() => (window as unknown as { e2eMarker?: boolean }).e2eMarker)).toBe(true)
})
