import { execFileSync } from 'node:child_process'

import { COMPOSE_FILE } from './env.ts'

/** Runs `docker compose` against the e2e stack only. */
export function compose(...args: string[]): string {
  return composeWithEnv({}, ...args)
}

/** Like compose, with extra variables for the compose file's ${...} substitutions. */
export function composeWithEnv(env: Record<string, string>, ...args: string[]): string {
  return execFileSync('docker', ['compose', '-f', COMPOSE_FILE, ...args], {
    env: { ...process.env, ...env },
    encoding: 'utf8',
    stdio: ['ignore', 'pipe', 'pipe'],
    timeout: 120_000,
  })
}

/** Log lines of one e2e service since `since` (an RFC 3339 time). */
export const serviceLogs = (service: string, since: string) =>
  compose('logs', '--no-color', '--no-log-prefix', '--since', since, service)

/** Recreates the auth service and both api instances with sign-up on or off, and waits until healthy. */
export function setSignupEnabled(enabled: boolean): void {
  composeWithEnv({ E2E_SIGNUP_ENABLED: String(enabled) }, 'up', '-d', '--wait', 'auth', 'api', 'api-ingest')
}
