import { defineConfig, devices } from '@playwright/test'

import { BASE_URL } from './fixtures/env.ts'

/**
 * End-to-end suite. Runs against the stack from docker-compose.e2e.yml
 * (`pnpm --filter @relaylab/e2e stack:up`); it never starts or targets
 * anything else. See docs/development/testing.md.
 */
export default defineConfig({
  testDir: './tests',
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  // Flakiness must surface, not be hidden: no retries in CI.
  retries: process.env.CI ? 0 : 1,
  workers: process.env.CI ? 2 : 4,
  timeout: 60_000,
  expect: { timeout: 10_000 },
  reporter: process.env.CI
    ? [['list'], ['html', { open: 'never' }]]
    : [['list'], ['html', { open: 'never' }]],
  use: {
    baseURL: BASE_URL,
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
    video: 'off',
  },
  projects: [
    {
      name: 'chromium',
      use: { ...devices['Desktop Chrome'] },
      testIgnore: /signup-disabled\.spec\.ts/,
    },
    {
      // Runs against the same stack restarted with AUTH_SIGNUP_ENABLED=false
      // (`pnpm --filter @relaylab/e2e e2e:signup-disabled`).
      name: 'signup-disabled',
      use: { ...devices['Desktop Chrome'] },
      testMatch: /signup-disabled\.spec\.ts/,
    },
  ],
})
