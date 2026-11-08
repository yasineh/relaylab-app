/**
 * Operator command: create a verified account, even while public sign-up is disabled.
 *
 *   pnpm --filter @relaylab/auth run create-user -- --email ada@example.com --name "Ada Lovelace"
 *
 * The password is read from standard input: typed twice without echo on a
 * terminal, or the first line when piped. It is never accepted as an argument
 * or environment variable, and never logged.
 */
import { parseArgs } from 'node:util'

import { createAuth } from '../auth.ts'
import { ConfigError, loadConfig } from '../config.ts'
import { CreateUserError, createVerifiedUser } from '../create-user.ts'
import { createPool } from '../db.ts'
import { createLogger } from '../logger.ts'
import { MemoryMailer } from '../mailer.ts'

const out = (s: string) => process.stderr.write(s)

function usage(): never {
  out('usage: create-user --email <address> --name <display name>\n(the password is read from stdin)\n')
  process.exit(2)
}

/** Reads one line from a TTY without echoing it. */
function readHidden(prompt: string): Promise<string> {
  return new Promise((resolve, reject) => {
    const stdin = process.stdin
    out(prompt)
    stdin.setRawMode(true)
    stdin.resume()
    stdin.setEncoding('utf8')
    let value = ''
    const done = (err?: Error) => {
      stdin.setRawMode(false)
      stdin.pause()
      stdin.removeListener('data', onData)
      out('\n')
      if (err) reject(err)
      else resolve(value)
    }
    const onData = (chunk: string) => {
      for (const ch of chunk) {
        if (ch === '\r' || ch === '\n') return done()
        if (ch === '\u0003') return done(new Error('cancelled'))
        if (ch === '\u007f' || ch === '\b') value = value.slice(0, -1)
        else value += ch
      }
    }
    stdin.on('data', onData)
  })
}

/** Reads the first line of piped stdin. */
async function readPipedLine(): Promise<string> {
  let data = ''
  for await (const chunk of process.stdin) {
    data += chunk
    if (data.includes('\n')) break
  }
  return data.split(/\r?\n/, 1)[0] ?? ''
}

async function readPassword(): Promise<string> {
  if (!process.stdin.isTTY) return readPipedLine()
  const first = await readHidden('Password: ')
  const second = await readHidden('Repeat password: ')
  if (first !== second) throw new CreateUserError('Passwords do not match.')
  return first
}

let args
try {
  args = parseArgs({ options: { email: { type: 'string' }, name: { type: 'string' } }, strict: true }).values
} catch {
  usage()
}
if (!args.email || !args.name) usage()

let config
try {
  config = loadConfig()
} catch (err) {
  out(`invalid configuration: ${err instanceof ConfigError ? err.problems.join('; ') : 'unknown error'}\n`)
  process.exit(1)
}

const logger = createLogger(config.logLevel)
const pool = createPool(config.databaseUrl.reveal(), 2)
const auth = createAuth({ config, pool, mailer: new MemoryMailer(), logger })
try {
  const password = await readPassword()
  const userId = await createVerifiedUser(auth, { email: args.email, name: args.name, password })
  logger.info({ userId }, 'operator created a verified user')
  out(`Created verified user ${userId}.\n`)
} catch (err) {
  out(`${err instanceof CreateUserError ? err.message : 'The account could not be created.'}\n`)
  if (!(err instanceof CreateUserError))
    logger.error({ err: { name: (err as Error).name } }, 'create-user failed')
  process.exitCode = 1
} finally {
  await pool.end()
}
