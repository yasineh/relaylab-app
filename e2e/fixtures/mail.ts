import { expect } from '@playwright/test'

import { MAILPIT_URL } from './env.ts'

type Summary = { ID: string; Subject: string }

/**
 * Waits for the verification email sent to `to` in the local Mailpit inbox
 * and returns the link in it. Mailpit is part of the e2e stack; nothing
 * leaves the machine.
 */
export async function verificationLink(to: string): Promise<string> {
  let id = ''
  await expect
    .poll(
      async () => {
        const res = await fetch(`${MAILPIT_URL}/api/v1/search?query=${encodeURIComponent(`to:"${to}"`)}`)
        const body = (await res.json()) as { messages: Summary[] }
        id = body.messages[0]?.ID ?? ''
        return id
      },
      { message: `verification email to ${to}`, timeout: 15_000 },
    )
    .not.toBe('')
  const msg = (await (await fetch(`${MAILPIT_URL}/api/v1/message/${id}`)).json()) as { Text: string }
  const link = /https?:\/\/\S+\/api\/auth\/verify-email\?\S+/.exec(msg.Text)?.[0]
  if (!link) throw new Error('no verification link in the email')
  return link
}
