import { createTransport } from 'nodemailer'
import type { Logger } from 'pino'

import type { SmtpConfig } from './config.ts'

export type MailTemplate = 'verify-email' | 'reset-password'

export interface MailMessage {
  to: string
  template: MailTemplate
  url: string
  /** Logged instead of the address. */
  userId: string
}

export interface Mailer {
  send(message: MailMessage): Promise<void>
}

interface Rendered {
  subject: string
  text: string
  html: string
}

const escapeHtml = (s: string) =>
  s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;')

export function render(template: MailTemplate, url: string): Rendered {
  const link = escapeHtml(url)
  switch (template) {
    case 'verify-email':
      return {
        subject: 'Verify your RelayLab email address',
        text: `Confirm your email address to finish setting up your RelayLab account:\n\n${url}\n\nThe link expires in 24 hours. If you did not create an account, ignore this email.\n`,
        html: `<p>Confirm your email address to finish setting up your RelayLab account:</p><p><a href="${link}">Verify email address</a></p><p>The link expires in 24 hours. If you did not create an account, ignore this email.</p>`,
      }
    case 'reset-password':
      return {
        subject: 'Reset your RelayLab password',
        text: `Someone asked to reset the password for your RelayLab account. To choose a new password, open:\n\n${url}\n\nThe link expires in 1 hour and works once. Resetting signs you out everywhere. If you did not ask for this, ignore this email.\n`,
        html: `<p>Someone asked to reset the password for your RelayLab account.</p><p><a href="${link}">Choose a new password</a></p><p>The link expires in 1 hour and works once. Resetting signs you out everywhere. If you did not ask for this, ignore this email.</p>`,
      }
  }
}

/** Sends mail over SMTP (Mailpit locally, a real provider in production). */
export function createSmtpMailer(smtp: SmtpConfig, from: string, logger: Logger): Mailer {
  const transport = createTransport({
    host: smtp.host,
    port: smtp.port,
    secure: smtp.secure,
    ...(smtp.user ? { auth: { user: smtp.user, pass: smtp.password?.reveal() ?? '' } } : {}),
    connectionTimeout: 10_000,
    greetingTimeout: 10_000,
    socketTimeout: 20_000,
  })
  return {
    async send({ to, template, url, userId }) {
      const { subject, text, html } = render(template, url)
      try {
        await transport.sendMail({ from, to, subject, text, html })
        logger.info({ template, userId }, 'mail sent')
      } catch (err) {
        // Never include the address, link, or SMTP error text (it can echo recipients).
        logger.error({ template, userId, code: (err as { code?: string }).code }, 'mail delivery failed')
        // The original error is deliberately not attached: its message can echo recipient addresses.
        // oxlint-disable-next-line preserve-caught-error
        throw new Error('mail delivery failed')
      }
    },
  }
}

/** Captures messages in memory. Used by tests and when no SMTP server is configured outside production. */
export class MemoryMailer implements Mailer {
  readonly messages: MailMessage[] = []
  readonly #logger: Logger | undefined
  constructor(logger?: Logger) {
    this.#logger = logger
  }
  async send(message: MailMessage): Promise<void> {
    this.messages.push(message)
    this.#logger?.info({ template: message.template, userId: message.userId }, 'mail captured in memory')
  }
  last(template?: MailTemplate): MailMessage | undefined {
    return this.messages.filter((m) => !template || m.template === template).at(-1)
  }
  clear(): void {
    this.messages.length = 0
  }
}
