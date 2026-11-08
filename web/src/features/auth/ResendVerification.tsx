import { useState } from 'react'

import { Button } from '@/components/ui/button'
import { mapAuthError } from '@/features/auth/errors'
import { authClient } from '@/lib/auth-client'

type State = 'idle' | 'sending' | 'sent' | 'error'

/** Requests a new verification email. The outcome text never reveals whether the account exists. */
export function ResendVerification({ email }: { email: string }) {
  const [state, setState] = useState<State>('idle')
  const [message, setMessage] = useState('')

  async function resend() {
    setState('sending')
    const { error } = await authClient.sendVerificationEmail({ email, callbackURL: '/verify-email' })
    if (error) {
      setState('error')
      setMessage(mapAuthError(error).message)
      return
    }
    setState('sent')
  }

  if (state === 'sent') {
    return (
      <output className="block text-sm">
        If the account needs verifying, a new link is on its way. Check your inbox.
      </output>
    )
  }
  return (
    <div className="space-y-1">
      <Button variant="outline" size="sm" onClick={() => void resend()} disabled={state === 'sending'}>
        {state === 'sending' ? 'Sending…' : 'Resend verification email'}
      </Button>
      {state === 'error' ? (
        <p role="alert" className="text-sm">
          {message}
        </p>
      ) : null}
    </div>
  )
}
