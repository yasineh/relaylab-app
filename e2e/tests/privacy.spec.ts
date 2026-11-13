import { randomUUID } from 'node:crypto'

import { createEndpoint, createProject, expect, sendWebhook, test } from '../fixtures/app.ts'

/**
 * Sensitive header values are removed when a webhook arrives (3.8). The UI
 * shows the header names with a redaction marker; the values appear nowhere:
 * not in the page, not in anything the copy buttons produce.
 */
test('credential header values never reach the UI or copied text', async ({ page, user: _u, request }) => {
  // Capture what the copy buttons write instead of reading the OS clipboard.
  await page.addInitScript(() => {
    const copied: string[] = []
    ;(window as unknown as { e2eCopied: string[] }).e2eCopied = copied
    Object.defineProperty(navigator, 'clipboard', {
      value: { writeText: async (t: string) => void copied.push(t) },
      configurable: true,
    })
  })
  const id = randomUUID()
  const secrets = [`e2e-secret-${id}`, `sid-${id}`, `key-${id}`]
  const project = await createProject(page)
  const endpoint = await createEndpoint(page, project.id)
  const res = await sendWebhook(request, endpoint.url, {
    path: '/private',
    headers: {
      Authorization: `Bearer ${secrets[0]}`,
      Cookie: `sid=${secrets[1]}`,
      'X-Api-Key': secrets[2]!,
      'Content-Type': 'application/json',
    },
    body: '{"ok":true}',
  })
  expect(res.status()).toBe(200)

  await page.goto(`/projects/${project.id}/events`)
  await page.getByRole('table', { name: 'Received events' }).getByRole('link', { name: '/private' }).click()
  await page.getByRole('tab', { name: /^Headers/ }).click()
  const headers = page.getByRole('tabpanel')
  for (const name of ['Authorization', 'Cookie', 'X-Api-Key']) {
    const row = headers.getByRole('row').filter({ hasText: new RegExp(`^${name}`, 'i') })
    await expect(row, name).toContainText('Removed at receipt')
  }

  await headers.getByRole('button', { name: 'Copy headers' }).click()
  await page.getByRole('button', { name: 'Copy as curl' }).click()
  await page.getByRole('tab', { name: 'Raw request' }).click()
  await page.getByRole('button', { name: 'Copy raw request' }).click()

  const copied = await page.evaluate(() => (window as unknown as { e2eCopied: string[] }).e2eCopied)
  expect(copied).toHaveLength(3)
  expect(copied[0]).toMatch(/Authorization/i)
  expect(copied[1]).toMatch(/^curl /)

  for (const tab of ['Overview', /^Body/, /^Headers/, /^Query/, 'Raw request'] as const) {
    await page.getByRole('tab', { name: tab }).click()
    const html = await page.content()
    for (const s of secrets) expect(html, `${String(tab)} tab`).not.toContain(s)
  }
  for (const text of copied) {
    for (const s of secrets) expect(text).not.toContain(s)
  }
})
