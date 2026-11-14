/**
 * Accessibility scan (release-hardening-review task 2.7): runs axe-core on the
 * main screens at 375 and 1280 px against the production-mode stack, signed in
 * as a real account with one project, endpoint, event, and replay.
 *
 *   docker compose -f docker-compose.prod.yml up -d --build --wait
 *   pnpm --filter @relaylab/e2e a11y-scan
 *
 * Exits non-zero when any page has a serious or critical violation. The browser
 * bypasses the CSP only so axe can be injected; the app is unchanged.
 */
import { execFileSync } from 'node:child_process'
import { randomUUID } from 'node:crypto'
import { readFileSync } from 'node:fs'
import { createRequire } from 'node:module'

import { chromium, type Page } from '@playwright/test'

const BASE = (process.env.RELAYLAB_DEMO_URL ?? 'https://localhost:8443').replace(/\/$/, '')
const ROOT = new URL('../../', import.meta.url).pathname
const axeSource = readFileSync(
  createRequire(`${ROOT}web/package.json`).resolve('axe-core/axe.min.js'),
  'utf8',
)

// A real account through the operator CLI (sign-up is closed in production).
const email = `a11y-${process.pid}@example.test`
const password = `a11y-cobalt-${randomUUID()}`
execFileSync(
  'docker',
  [
    'compose',
    '-f',
    `${ROOT}docker-compose.prod.yml`,
    'exec',
    '-T',
    'auth',
    'node',
    'src/cli/create-user.ts',
    '--email',
    email,
    '--name',
    'Accessibility Review',
  ],
  { input: `${password}\n`, stdio: ['pipe', 'ignore', 'inherit'] },
)

const browser = await chromium.launch()
const context = await browser.newContext({ baseURL: BASE, ignoreHTTPSErrors: true, bypassCSP: true })
const api = context.request
const call = async (path: string, init: Parameters<typeof api.fetch>[1] = {}) => {
  const res = await api.fetch(BASE + path, { ...init, headers: { Origin: BASE, ...init.headers } })
  if (!res.ok()) throw new Error(`${path}: ${res.status()}`)
  return res.status() === 204 ? null : res.json()
}
await call('/api/auth/sign-in/email', { method: 'POST', data: { email, password } })
const project = await call('/api/v1/projects', { method: 'POST', data: { name: 'Accessibility' } })
const endpoint = await call(`/api/v1/projects/${project.id}/endpoints`, {
  method: 'POST',
  data: { name: 'Inbound' },
})
const sink = await call(`/api/v1/projects/${project.id}/endpoints`, {
  method: 'POST',
  data: { name: 'Sink' },
})
const hook = await api.fetch(`${endpoint.url}/orders?attempt=1`, {
  method: 'POST',
  headers: { 'Content-Type': 'application/json', Authorization: 'Bearer review' },
  data: JSON.stringify({ id: 'evt_a11y', items: [{ sku: 'A-1', qty: 2 }] }),
})
const { event_id: eventId } = await hook.json()
const replay = await call(`/api/v1/projects/${project.id}/events/${eventId}/replays`, {
  method: 'POST',
  headers: { 'Idempotency-Key': randomUUID() },
  data: { destination: { type: 'sink', endpoint_id: sink.id } },
})

const signedOut = await browser.newContext({ baseURL: BASE, ignoreHTTPSErrors: true, bypassCSP: true })
const pages: [string, string, 'in' | 'out'][] = [
  ['Landing', '/', 'out'],
  ['Security and privacy', '/security', 'out'],
  ['Sign in', '/sign-in', 'out'],
  ['Projects', '/projects', 'in'],
  ['Project overview', `/projects/${project.id}`, 'in'],
  ['Endpoint detail', `/projects/${project.id}/endpoints/${endpoint.id}`, 'in'],
  ['Event explorer', `/projects/${project.id}/events`, 'in'],
  ['Event detail', `/projects/${project.id}/events/${eventId}`, 'in'],
  ['Event headers', `/projects/${project.id}/events/${eventId}?tab=headers`, 'in'],
  ['Replay history', `/projects/${project.id}/replays?replay=${replay.id}`, 'in'],
  ['Account settings', '/settings/account', 'in'],
]

type Violation = { id: string; impact: string; help: string; nodes: unknown[] }
let blocking = 0
for (const width of [375, 1280]) {
  for (const [name, path, auth] of pages) {
    const page: Page = await (auth === 'in' ? context : signedOut).newPage()
    await page.setViewportSize({ width, height: 900 })
    await page.goto(path)
    await page.getByRole('main').waitFor()
    await page.waitForLoadState('networkidle')
    await page.addScriptTag({ content: axeSource })
    const violations: Violation[] = await page.evaluate(async () => {
      const axe = (window as unknown as { axe: { run: (o: object) => Promise<{ violations: Violation[] }> } })
        .axe
      const result = await axe.run({
        runOnly: {
          type: 'tag',
          values: ['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa', 'wcag22aa', 'best-practice'],
        },
      })
      return result.violations
    })
    const serious = violations.filter((v) => v.impact === 'serious' || v.impact === 'critical')
    blocking += serious.length
    const summary = violations.map((v) => `${v.impact} ${v.id} (${v.nodes.length})`).join(', ') || 'none'
    process.stdout.write(`${serious.length ? 'FAIL' : 'PASS'}  ${width}px  ${name}: ${summary}\n`)
    for (const v of serious) process.stdout.write(`        ${v.id}: ${v.help}\n`)
    await page.close()
  }
}
await browser.close()
process.stdout.write(`\n${blocking} serious or critical violation(s)\n`)
process.exitCode = blocking ? 1 : 0
