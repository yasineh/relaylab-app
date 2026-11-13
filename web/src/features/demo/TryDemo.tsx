import { useQueryClient } from '@tanstack/react-query'
import { FlaskConical, Loader2 } from 'lucide-react'
import { useState } from 'react'
import { useNavigate } from 'react-router'

import { Alert } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { resetUserData } from '@/hooks/session'
import { apiFetch } from '@/lib/api'
import { authClient } from '@/lib/auth-client'

/**
 * "Try the demo": signs in as a demo user (a Better Auth anonymous user) and
 * opens the demo project, which the API seeds on the first request.
 */
export function TryDemoButton({ size = 'default' }: { size?: 'default' | 'sm' | 'lg' }) {
  const client = useQueryClient()
  const navigate = useNavigate()
  const [pending, setPending] = useState(false)
  const [error, setError] = useState<string | null>(null)

  async function start() {
    setPending(true)
    setError(null)
    const { error: err } = await authClient.signIn.anonymous()
    if (err) {
      setPending(false)
      setError(
        err.status === 429
          ? 'Too many demos were started from your network. Try again in an hour.'
          : 'The demo could not be started. Try again.',
      )
      return
    }
    await resetUserData(client)
    try {
      const { items } = await apiFetch<{ items: { id: string }[] }>('/api/v1/projects')
      void navigate(items[0] ? `/projects/${items[0].id}/events` : '/projects', { replace: true })
    } catch {
      void navigate('/projects', { replace: true })
    }
  }

  return (
    <div className="space-y-2">
      <Button size={size} onClick={() => void start()} disabled={pending}>
        {pending ? (
          <Loader2 aria-hidden="true" className="animate-spin" />
        ) : (
          <FlaskConical aria-hidden="true" />
        )}
        {pending ? 'Starting the demo…' : 'Try the demo'}
      </Button>
      {error ? <Alert tone="error">{error}</Alert> : null}
    </div>
  )
}
