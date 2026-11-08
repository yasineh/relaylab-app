import { spawn } from 'node:child_process'
import { fileURLToPath } from 'node:url'

import { afterAll, beforeAll, describe, expect, it } from 'vitest'

import { createTestDatabase, type TestDatabase } from './helpers/db.ts'
import {
  TEST_ORIGIN,
  TEST_PROXY_SECRET,
  TEST_SECRET,
  migrateTestDatabase,
  startAuth,
  type Harness,
} from './helpers/harness.ts'

const CLI = fileURLToPath(new URL('../src/cli/create-user.ts', import.meta.url))
const PASSWORD = 'violet-anchor-89-orbit'

let db: TestDatabase
let h: Harness

beforeAll(async () => {
  db = await createTestDatabase()
  await migrateTestDatabase(db)
  // Production-like: public sign-up disabled.
  h = await startAuth(db, { signupEnabled: false })
})
afterAll(async () => {
  await h?.close()
  await db?.drop()
})

function runCli(args: string[], stdin: string): Promise<{ code: number | null; output: string }> {
  return new Promise((resolve, reject) => {
    const child = spawn(process.execPath, [CLI, ...args], {
      env: {
        PATH: process.env.PATH,
        APP_ENV: 'test',
        APP_ORIGIN: TEST_ORIGIN,
        AUTH_DATABASE_URL: db.url('auth_runtime'),
        BETTER_AUTH_SECRET: TEST_SECRET,
        AUTH_PROXY_SHARED_SECRET: TEST_PROXY_SECRET,
        AUTH_SIGNUP_ENABLED: 'false',
        LOG_LEVEL: 'info',
      },
      stdio: ['pipe', 'pipe', 'pipe'],
    })
    let output = ''
    child.stdout.on('data', (d) => (output += d))
    child.stderr.on('data', (d) => (output += d))
    child.on('error', reject)
    child.on('close', (code) => resolve({ code, output }))
    child.stdin.end(stdin)
  })
}

describe('create-user CLI', () => {
  it('creates a verified user from a piped password while sign-up is disabled, and they can sign in', async () => {
    const email = 'Operator@Example.test'
    const res = await runCli(['--email', email, '--name', 'First Operator'], `${PASSWORD}\n`)
    expect(res.code).toBe(0)
    expect(res.output).toMatch(/Created verified user \S+\./)
    // The password and email never appear in the output (which includes the JSON log line).
    expect(res.output).not.toContain(PASSWORD)
    expect(res.output.toLowerCase()).not.toContain('operator@example.test')

    const { rows } = await h.pool.query('SELECT email, name, "emailVerified" FROM auth."user"')
    expect(rows).toEqual([{ email: 'operator@example.test', name: 'First Operator', emailVerified: true }])

    const signIn = await h.call('/api/auth/sign-in/email', {
      body: { email: 'operator@example.test', password: PASSWORD },
    })
    expect(signIn.status).toBe(200)
    expect(signIn.sessionCookie).toBeDefined()
  })

  it('refuses a duplicate email', async () => {
    const res = await runCli(['--email', 'operator@example.test', '--name', 'Again'], `${PASSWORD}\n`)
    expect(res.code).toBe(1)
    expect(res.output).toContain('already exists')
  })

  it.each([
    ['a short password', 'short', /12–128 characters/],
    ['a common password', 'qwerty123456', /too common/],
  ])('refuses %s', async (_label, pw, message) => {
    const res = await runCli(['--email', `x${Date.now()}@example.test`, '--name', 'X'], `${pw}\n`)
    expect(res.code).toBe(1)
    expect(res.output).toMatch(message)
  })

  it('does not accept a password argument', async () => {
    const res = await runCli(['--email', 'y@example.test', '--name', 'Y', '--password', PASSWORD], '')
    expect(res.code).toBe(2)
    expect(res.output).toContain('usage')
  })
})
