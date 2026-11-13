/** Where the e2e stack (docker-compose.e2e.yml) listens on the host. */
const port = (name: string, fallback: number) => Number(process.env[name] ?? fallback)

export const BASE_URL = `http://localhost:${port('E2E_API_PORT', 4180)}`
/** A second api instance on the same database, used while the main api restarts. */
export const INGEST_URL = `http://localhost:${port('E2E_INGEST_PORT', 4181)}`
export const MAILPIT_URL = `http://localhost:${port('E2E_MAILPIT_PORT', 4125)}`
/** Superuser on the e2e Postgres (test-only placeholder password). */
export const DATABASE_URL = `postgres://postgres:e2e-only-postgres@localhost:${port('E2E_POSTGRES_PORT', 4132)}/relaylab`
export const COMPOSE_FILE = new URL('../../docker-compose.e2e.yml', import.meta.url).pathname

/** Values docker-compose.e2e.yml sets; tests assert behaviour at these limits. */
export const LIMITS = {
  ingestMaxBodyBytes: 65536,
  endpointBurst: 10,
}

/** A made-up passphrase that passes the password rules. Not a credential. */
export const PASSWORD = 'e2e-copper-kettle-73-north'
