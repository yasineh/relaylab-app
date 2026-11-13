import { createEndpoint, createProject, expect, sendWebhook, test } from '../fixtures/app.ts'

/**
 * The worker runs on a network with no route to the internet
 * (docker-compose.e2e.yml), so the suite never depends on public services
 * (4.2). A replay to a real public host passes every policy check and then
 * fails to connect, instead of reaching the internet.
 */
test('the worker cannot reach the internet: a public destination fails to connect', async ({
  page,
  user: _u,
  request,
}) => {
  const project = await createProject(page)
  const endpoint = await createEndpoint(page, project.id)
  await sendWebhook(request, endpoint.url, { path: '/offline', body: 'x' })
  await page.goto(`/projects/${project.id}/events`)
  await page.getByRole('table', { name: 'Received events' }).getByRole('link', { name: '/offline' }).click()

  await page.getByRole('button', { name: 'Replay' }).click()
  const dialog = page.getByRole('dialog', { name: 'Replay event' })
  await dialog.getByRole('radio', { name: 'URL' }).check()
  await dialog.getByLabel('Destination URL').fill('https://example.com/webhooks')
  await dialog.getByRole('button', { name: 'Send replay' }).click()
  await expect(dialog).toBeHidden()

  const replay = page.getByRole('list', { name: 'Replays of this event' }).getByRole('listitem')
  await expect(replay).toContainText('Failed', { timeout: 30_000 })
  await expect(replay).not.toContainText('Succeeded')
})
