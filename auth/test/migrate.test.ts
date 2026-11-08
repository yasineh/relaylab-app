import { Client } from 'pg'
import { afterAll, beforeAll, describe, expect, it } from 'vitest'

import { createLogger } from '../src/logger.ts'
import { migrate } from '../src/migrate.ts'
import { createTestDatabase, type TestDatabase } from './helpers/db.ts'

let db: TestDatabase
const logger = createLogger('silent')

beforeAll(async () => {
  db = await createTestDatabase()
})
afterAll(async () => {
  await db?.drop()
})

async function query<T = Record<string, unknown>>(url: string, sql: string, params: unknown[] = []) {
  const c = new Client({ connectionString: url })
  await c.connect()
  try {
    return (await c.query(sql, params)).rows as T[]
  } finally {
    await c.end()
  }
}

describe('auth migrate command', () => {
  it('creates the Better Auth tables on a fresh database, then is a no-op the second time', async () => {
    const first = await migrate(db.url('auth_migrator'), logger)
    expect(first.created.sort()).toEqual(['account', 'rateLimit', 'session', 'user', 'verification'])

    const second = await migrate(db.url('auth_migrator'), logger)
    expect(second).toEqual({ created: [], altered: [] })
  })

  it('creates tables in the auth schema, owned by auth_migrator', async () => {
    const rows = await query<{ schemaname: string; tablename: string; tableowner: string }>(
      db.adminUrl,
      `SELECT schemaname, tablename, tableowner FROM pg_tables WHERE tablename IN ('user','session','account','verification','rateLimit') ORDER BY tablename`,
    )
    expect(rows).toHaveLength(5)
    for (const r of rows) {
      expect(r.schemaname).toBe('auth')
      expect(r.tableowner).toBe('auth_migrator')
    }
  })

  it('has a unique index on the session token', async () => {
    const rows = await query<{ indexdef: string }>(
      db.adminUrl,
      `SELECT indexdef FROM pg_indexes WHERE schemaname = 'auth' AND tablename = 'session'`,
    )
    expect(rows.some((r) => /UNIQUE INDEX .* \(token\)/.test(r.indexdef))).toBe(true)
  })

  it('grants app_runtime SELECT only on session and user', async () => {
    const tables = ['user', 'session', 'account', 'verification', 'rateLimit']
    for (const t of tables) {
      const [row] = await query<Record<string, boolean>>(
        db.adminUrl,
        `SELECT has_table_privilege('app_runtime', $1, 'SELECT') AS sel,
                has_table_privilege('app_runtime', $1, 'INSERT') AS ins,
                has_table_privilege('app_runtime', $1, 'UPDATE') AS upd,
                has_table_privilege('app_runtime', $1, 'DELETE') AS del`,
        [`auth."${t}"`],
      )
      const readable = t === 'user' || t === 'session'
      expect({ table: t, ...row }).toEqual({ table: t, sel: readable, ins: false, upd: false, del: false })
    }
  })

  it('grants app_migrator REFERENCES on auth.user for app foreign keys', async () => {
    const [row] = await query<{ ref: boolean }>(
      db.adminUrl,
      `SELECT has_table_privilege('app_migrator', 'auth."user"', 'REFERENCES') AS ref`,
    )
    expect(row?.ref).toBe(true)
  })

  it('lets auth_runtime read and write the auth tables', async () => {
    const [row] = await query<Record<string, boolean>>(
      db.adminUrl,
      `SELECT has_table_privilege('auth_runtime', 'auth."session"', 'INSERT') AS ins,
              has_table_privilege('auth_runtime', 'auth."session"', 'DELETE') AS del,
              has_table_privilege('auth_runtime', 'auth."rateLimit"', 'UPDATE') AS upd`,
    )
    expect(row).toEqual({ ins: true, del: true, upd: true })
  })
})
