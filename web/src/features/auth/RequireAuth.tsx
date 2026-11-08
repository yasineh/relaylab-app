import { useQueryClient } from '@tanstack/react-query'
import { Loader2, MailWarning } from 'lucide-react'
import { useState } from 'react'
import { Navigate, Outlet, useLocation, useNavigate } from 'react-router'

import { AuthLayout } from '@/components/auth-layout'
import { Alert } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { ResendVerification } from '@/features/auth/ResendVerification'
import { useMe } from '@/hooks/session'
import { isApiError } from '@/lib/api'
import { authClient } from '@/lib/auth-client'
import { signInPath } from '@/lib/redirect'

/**
 * Guards dashboard routes. The Go API (GET /api/v1/me) is the authority: a
 * signed-out visitor is sent to sign-in with the requested path in `next`, an
 * unverified user sees how to verify, and losing the session mid-use returns
 * to sign-in with a "session expired" notice.
 */
export function RequireAuth() {
  const me = useMe()
  const location = useLocation()
  // Whether this visit was signed in at some point: losing the session then shows a notice.
  const [hadUser, setHadUser] = useState(false)
  if (me.data && !hadUser) setHadUser(true)
  // Keeps the Better Auth session refreshed (on load and focus); never used for authorization.
  authClient.useSession()

  const here = location.pathname + location.search + location.hash

  if (me.isPending) {
    return (
      <output className="flex items-center justify-center gap-2 py-16 text-muted-foreground">
        <Loader2 aria-hidden="true" className="size-4 animate-spin" /> Loading…
      </output>
    )
  }
  if (me.data === null) {
    return <Navigate to={signInPath(here, hadUser ? 'expired' : undefined)} replace />
  }
  if (me.isError) {
    if (isApiError(me.error) && me.error.code === 'email_not_verified') return <UnverifiedState />
    if (isApiError(me.error) && me.error.status === 401) {
      // The global handler is already redirecting with the "session expired" notice.
      return null
    }
    return (
      <div className="mx-auto max-w-md py-16">
        <Alert tone="error">
          <p>RelayLab could not load your account. Check your connection and try again.</p>
          <Button variant="outline" size="sm" onClick={() => void me.refetch()}>
            Try again
          </Button>
        </Alert>
      </div>
    )
  }
  return <Outlet />
}

function UnverifiedState() {
  const session = authClient.useSession()
  const email = session.data?.user.email
  const queryClient = useQueryClient()
  const navigate = useNavigate()
  async function signOut() {
    await authClient.signOut()
    queryClient.clear()
    void navigate('/sign-in?reason=signed-out', { replace: true })
  }
  return (
    <AuthLayout title="Verify your email to continue">
      <div className="space-y-4 text-sm">
        <MailWarning aria-hidden="true" className="size-8 text-primary" />
        <p>Open the verification link we emailed you to start using RelayLab. Links expire after 24 hours.</p>
        {email ? <ResendVerification email={email} /> : null}
        <Button variant="ghost" size="sm" onClick={() => void signOut()}>
          Sign out
        </Button>
      </div>
    </AuthLayout>
  )
}
