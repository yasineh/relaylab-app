/**
 * CI check: run the real migrate command twice on a fresh database with the
 * production roles applied, proving migrations work from scratch and are idempotent.
 *
 * Needs CI_ADMIN_DATABASE_URL (a superuser URL on a throwaway CI cluster).
 */
import { execFileSync } from 'node:child_process'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'

import { Client } from 'pg'

const admin = process.env.CI_ADMIN_DATABASE_URL?.trim()
if (!admin) {
  process.stderr.write('CI_ADMIN_DATABASE_URL is required\n')
  process.exit(1)
}

// Throwaway credentials for the ephemeral CI database only.
const passwords: Record<string, string> = {
  app_migrator: 'ci-only-app-migrator',
  app_runtime: 'ci-only-app-runtime',
  auth_migrator: 'ci-only-auth-migrator',
  auth_runtime: 'ci-only-auth-runtime',
}
const dbName = 'relaylab_ci_auth_migrate'
const withDb = (role?: string) => {
  const u = new URL(admin)
  u.pathname = `/${dbName}`
  if (role) {
    u.username = role
    u.password = passwords[role] ?? ''
  }
  return u.toString()
}

const c = new Client({ connectionString: admin })
await c.connect()
await c.query(`DROP DATABASE IF EXISTS ${dbName} WITH (FORCE)`)
await c.query(`CREATE DATABASE ${dbName}`)
await c.end()

const roles = new Client({ connectionString: withDb() })
await roles.connect()
for (const [role, pw] of Object.entries(passwords)) {
  await roles.query('SELECT set_config($1, $2, false)', [`relaylab.${role}_password`, pw])
}
await roles.query(
  readFileSync(fileURLToPath(new URL('../../../deploy/sql/roles.sql', import.meta.url)), 'utf8'),
)
await roles.end()

const cli = fileURLToPath(new URL('./migrate.ts', import.meta.url))
for (const run of [1, 2]) {
  process.stdout.write(`migrate run ${run}\n`)
  execFileSync(process.execPath, [cli], {
    stdio: 'inherit',
    env: { ...process.env, AUTH_MIGRATION_DATABASE_URL: withDb('auth_migrator') },
  })
}

const check = new Client({ connectionString: withDb() })
await check.connect()
const { rows } = await check.query<{ sel_session: boolean; sel_user: boolean; write_session: boolean }>(
  `SELECT has_table_privilege('app_runtime', 'auth."session"', 'SELECT') AS sel_session,
          has_table_privilege('app_runtime', 'auth."user"', 'SELECT') AS sel_user,
          has_table_privilege('app_runtime', 'auth."session"', 'INSERT,UPDATE,DELETE') AS write_session`,
)
await check.end()
const g = rows[0]
if (!g || !g.sel_session || !g.sel_user || g.write_session) {
  process.stderr.write(`unexpected app_runtime grants: ${JSON.stringify(g)}\n`)
  process.exit(1)
}
process.stdout.write('auth migrations: fresh + idempotent run OK, app_runtime grants OK\n')
