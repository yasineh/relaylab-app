import { pino, type DestinationStream, type Logger } from 'pino'

/**
 * Structured JSON logging for the auth service.
 *
 * Rules (spec: credential-safe logging): never log passwords, tokens, cookies,
 * links, secrets, or email addresses. Users are identified by id only. Request
 * bodies are never logged. Redaction is a safety net; call sites log only safe
 * fields in the first place.
 */
export const REDACT_PATHS = [
  'req.headers.cookie',
  'req.headers.authorization',
  'req.headers["x-relaylab-proxy"]',
  'res.headers["set-cookie"]',
  'headers.cookie',
  'headers.authorization',
  'headers["set-cookie"]',
  'headers["x-relaylab-proxy"]',
  '*.password',
  '*.newPassword',
  '*.currentPassword',
  '*.token',
  '*.email',
  '*.url',
  '*.secret',
  '*.body',
  'password',
  'token',
  'email',
  'url',
  'secret',
  'body',
]

/** pino level labels to Cloud Logging severities. */
const SEVERITY: Record<string, string> = {
  trace: 'DEBUG',
  debug: 'DEBUG',
  info: 'INFO',
  warn: 'WARNING',
  error: 'ERROR',
  fatal: 'CRITICAL',
}

export function createLogger(level: string, destination?: DestinationStream): Logger {
  return pino(
    {
      level,
      base: { service: 'relaylab-auth' },
      timestamp: pino.stdTimeFunctions.isoTime,
      // "severity" is what Cloud Logging reads; "level" stays for local reading.
      formatters: { level: (label) => ({ level: label, severity: SEVERITY[label] ?? 'DEFAULT' }) },
      redact: { paths: REDACT_PATHS, censor: '[REDACTED]' },
    },
    destination,
  )
}

const EMAIL_RE = /[^\s@"'<>]+@[^\s@"'<>]+\.[^\s@"'<>]+/g
const URL_RE = /\bhttps?:\/\/\S+/g
const TOKENISH_RE = /\b(token|password|secret|cookie)=\S+/gi

/**
 * Removes anything that looks like an email address, URL, or credential from a
 * free-text message. Used for messages produced by third-party code (Better
 * Auth's internal logger) whose content we do not control.
 */
export function scrub(message: string): string {
  return message.replace(URL_RE, '[url]').replace(EMAIL_RE, '[email]').replace(TOKENISH_RE, '$1=[REDACTED]')
}
