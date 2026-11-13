/**
 * Captures the landing page's product screenshots from a demo workspace, so
 * every pixel is real RelayLab UI showing the synthetic seed data
 * (backend/internal/demo/seed.json). Run against the e2e stack:
 *
 *   pnpm --filter @relaylab/e2e stack:up
 *   pnpm --filter @relaylab/e2e screenshots
 *
 * Output: web/src/assets/screenshots/{receive,inspect,replay}.jpg
 */
import { chromium } from '@playwright/test'

import { BASE_URL } from '../fixtures/env.ts'

const OUT = new URL('../../web/src/assets/screenshots/', import.meta.url).pathname

const browser = await chromium.launch()
const context = await browser.newContext({
  baseURL: BASE_URL,
  viewport: { width: 1100, height: 800 },
  deviceScaleFactor: 2,
  colorScheme: 'light',
  reducedMotion: 'reduce',
})
const page = await context.newPage()
const api = context.request
const json = async (path: string, init?: Parameters<typeof api.fetch>[1]) => {
  const res = await api.fetch(BASE_URL + path, { headers: { Origin: BASE_URL }, ...init })
  if (!res.ok()) throw new Error(`${path}: ${res.status()}`)
  return res.json()
}

await json('/api/auth/sign-in/anonymous', { method: 'POST', data: {} })
const project = (await json('/api/v1/projects')).items[0]
const events = (await json(`/api/v1/projects/${project.id}/events?limit=100`)).items
const endpoints = (await json(`/api/v1/projects/${project.id}/endpoints`)).items
const sink = endpoints.find((e: { name: string }) => e.name === 'Test sink')
// /orders arrived with an Authorization header and a token in the query, both removed at receipt.
const orders = events.find((e: { path: string }) => e.path === '/orders')

async function shot(name: string, target: ReturnType<typeof page.locator>) {
  await page.locator('body').evaluate(() => document.fonts.ready)
  await target.screenshot({ path: `${OUT}${name}.jpg`, type: 'jpeg', quality: 82, animations: 'disabled' })
}

// 1. Receive: the live event list.
await page.goto(`/projects/${project.id}/events`)
await page.getByText('Live', { exact: true }).waitFor()
const table = page.getByRole('table', { name: 'Received events' })
await table.waitFor()
await shot('receive', table)

// 2. Inspect: the headers of /orders (credentials removed at receipt).
await page.goto(`/projects/${project.id}/events/${orders.id}?tab=headers`)
await page.getByRole('tabpanel').waitFor()
await shot('inspect', page.getByRole('tablist', { name: 'Event details' }).locator('..'))

// 3. Replay: a sink replay and its result in replay history.
const replay = await json(`/api/v1/projects/${project.id}/events/${orders.id}/replays`, {
  method: 'POST',
  headers: { Origin: BASE_URL, 'Idempotency-Key': crypto.randomUUID() },
  data: { destination: { type: 'sink', endpoint_id: sink.id } },
})
for (let i = 0; i < 40; i++) {
  const r = await json(`/api/v1/projects/${project.id}/replays/${replay.id}`)
  if (r.state === 'succeeded') break
  await page.waitForTimeout(250)
}
await page.goto(`/projects/${project.id}/replays?replay=${replay.id}`)
const response = page.getByRole('heading', { name: /^Response \d+/ })
await response.waitFor()
await page.evaluate(() => (document.activeElement as HTMLElement | null)?.blur())
await shot('replay', page.getByRole('dialog', { name: 'Replay details' }))

await browser.close()
console.warn(`screenshots written to ${OUT}`)
