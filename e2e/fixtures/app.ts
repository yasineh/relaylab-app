import { randomUUID } from 'node:crypto'

import { test as base, expect, type APIRequestContext, type APIResponse, type Page } from '@playwright/test'

import { BASE_URL, PASSWORD } from './env.ts'
import { verificationLink } from './mail.ts'

export type User = { email: string; name: string }
export type Project = { id: string; name: string }
export type Endpoint = { id: string; name: string; url: string }

/** A unique address per call, so parallel tests and repeated runs never collide. */
export const uniqueEmail = () => `e2e+${randomUUID()}@example.test`
export const uniqueName = (prefix: string) => `${prefix} ${randomUUID().slice(0, 8)}`

/** Same-origin JSON request with the page's session cookie (the dashboard API's CSRF rules). */
async function apiCall<T>(page: Page, method: string, path: string, data?: unknown): Promise<T> {
  const res = await page.request.fetch(BASE_URL + path, {
    method,
    headers: { Origin: BASE_URL, ...(data === undefined ? {} : { 'Content-Type': 'application/json' }) },
    ...(data === undefined ? {} : { data }),
  })
  expect(res.ok(), `${method} ${path}: ${res.status()}`).toBe(true)
  return (res.status() === 204 ? undefined : await res.json()) as T
}

/**
 * Signs up through the real Better Auth service and verifies the email with
 * the link from Mailpit, leaving `page` signed in. The UI sign-up form is
 * exercised by the journey test; other tests use this faster path.
 */
export async function signUp(page: Page): Promise<User> {
  const user = { email: uniqueEmail(), name: 'E2E Tester' }
  const res = await page.request.post(`${BASE_URL}/api/auth/sign-up/email`, {
    headers: { Origin: BASE_URL },
    data: { ...user, password: PASSWORD, callbackURL: '/verify-email' },
  })
  expect(res.ok(), `sign-up: ${res.status()}`).toBe(true)
  await page.goto(await verificationLink(user.email))
  await expect(page.getByRole('heading', { name: 'Email verified' })).toBeVisible()
  await expect(page.getByRole('link', { name: 'Continue to RelayLab' })).toBeVisible()
  return user
}

export const createProject = (page: Page, name = uniqueName('Project')) =>
  apiCall<Project>(page, 'POST', '/api/v1/projects', { name })

export const createEndpoint = (page: Page, projectId: string, name = uniqueName('Endpoint')) =>
  apiCall<Endpoint>(page, 'POST', `/api/v1/projects/${projectId}/endpoints`, { name })

export type Webhook = {
  method?: string
  path?: string
  query?: string
  headers?: Record<string, string>
  body?: string | Buffer
}

/** Sends a webhook like curl would: no cookies, no browser. */
export function sendWebhook(
  request: APIRequestContext,
  endpointUrl: string,
  w: Webhook = {},
): Promise<APIResponse> {
  const url = endpointUrl + (w.path ?? '') + (w.query ? `?${w.query}` : '')
  return request.fetch(url, {
    method: w.method ?? 'POST',
    headers: w.headers ?? {},
    ...(w.body === undefined ? {} : { data: w.body }),
    failOnStatusCode: false,
  })
}

/** Rewrites an endpoint URL to another host (the ingest-only api instance). */
export const viaHost = (endpointUrl: string, base: string) => base + new URL(endpointUrl).pathname

type Fixtures = {
  /** A freshly signed-up, verified, signed-in user in `page`. */
  user: User
}

export const test = base.extend<Fixtures>({
  user: async ({ page }, use) => {
    await use(await signUp(page))
  },
})

export { expect }
