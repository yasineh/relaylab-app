import { randomBytes } from 'node:crypto'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'

import { Client } from 'pg'

/**
 * Throwaway databases for integration tests, mirroring backend dbtest.
 *
 * RELAYLAB_TEST_DATABASE_URL is a superuser URL for the isolated postgres-test
 * cluster (scripts/test.sh starts it). Roles are cluster-wide, so tests never
 * run against the development database server.
 */

// Test-only role passwords, identical to backend/internal/platform/db/dbtest.
export const TEST_PASSWORDS = {
  app_migrator: 'test-only-app-migrator',
  app_runtime: 'test-only-app-runtime',
  auth_migrator: 'test-only-auth-migrator',
  auth_runtime: 'test-only-auth-runtime',
} as const

type Role = keyof typeof TEST_PASSWORDS

export interface TestDatabase {
  name: string
  adminUrl: string
  url(role: Role): string
  drop(): Promise<void>
}

const ROLES_SQL = fileURLToPath(new URL('../../../deploy/sql/roles.sql', import.meta.url))
const ROLE_LOCK = 727001 // shared with backend dbtest

export function testDatabaseUrl(): string {
  const base = process.env.RELAYLAB_TEST_DATABASE_URL
  if (!base)
    throw new Error(
      'RELAYLAB_TEST_DATABASE_URL is required; run tests with `pnpm --filter @relaylab/auth test`',
    )
  return base
}

function withDb(base: string, name: string, role?: Role): string {
  const u = new URL(base)
  u.pathname = `/${name}`
  if (role) {
    u.username = role
    u.password = TEST_PASSWORDS[role]
  }
  return u.toString()
}

/**
 * Applies deploy/sql/roles.sql. Roles are cluster-wide, so concurrent test files
 * serialise on an advisory lock held on the shared base database (advisory locks
 * are per database, so the lock must not be taken in the per-test database).
 */
/**
 * Runs fn while holding the cluster-wide advisory lock that serialises role and
 * grant changes. Advisory locks are per database, so it is taken on the shared
 * base database; the Go suites use the same key (727001).
 */
export async function withRoleLock<T>(fn: () => Promise<T>): Promise<T> {
  const lock = new Client({ connectionString: testDatabaseUrl() })
  await lock.connect()
  try {
    await lock.query('SELECT pg_advisory_lock($1)', [ROLE_LOCK])
    return await fn()
  } finally {
    await lock.query('SELECT pg_advisory_unlock($1)', [ROLE_LOCK]).catch(() => {})
    await lock.end()
  }
}

/** Applies deploy/sql/roles.sql under the role lock (roles are cluster-wide). */
export async function applyRoles(adminUrl: string): Promise<void> {
  await withRoleLock(async () => {
    const c = new Client({ connectionString: adminUrl })
    await c.connect()
    try {
      for (const [role, pw] of Object.entries(TEST_PASSWORDS)) {
        await c.query(`SELECT set_config($1, $2, false)`, [`relaylab.${role}_password`, pw])
      }
      await c.query(readFileSync(ROLES_SQL, 'utf8'))
    } finally {
      await c.end()
    }
  })
}

export async function createTestDatabase(): Promise<TestDatabase> {
  const base = testDatabaseUrl()
  const name = `relaylab_auth_test_${randomBytes(6).toString('hex')}`
  // CREATE/DROP DATABASE touch shared catalogs too: run them under the role lock.
  await withRoleLock(async () => {
    const admin = new Client({ connectionString: base })
    await admin.connect()
    try {
      await admin.query(`CREATE DATABASE "${name}"`)
    } finally {
      await admin.end()
    }
  })
  const adminUrl = withDb(base, name)
  await applyRoles(adminUrl)
  return {
    name,
    adminUrl,
    url: (role) => withDb(base, name, role),
    async drop() {
      await withRoleLock(async () => {
        const c = new Client({ connectionString: base })
        await c.connect()
        try {
          await c.query(`DROP DATABASE IF EXISTS "${name}" WITH (FORCE)`)
        } finally {
          await c.end()
        }
      })
    },
  }
}
