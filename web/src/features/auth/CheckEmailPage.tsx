import { MailCheck } from 'lucide-react'
import { Link, useLocation } from 'react-router'

import { AuthLayout } from '@/components/auth-layout'
import { ResendVerification } from '@/features/auth/ResendVerification'

/**
 * Shown after sign-up when verification is required. The email comes from router
 * state (never the URL), and the text is identical for new and existing accounts.
 */
export function CheckEmailPage() {
  const location = useLocation()
  const email = (location.state as { email?: unknown } | null)?.email
  const address = typeof email === 'string' ? email : null
  return (
    <AuthLayout
      title="Check your email"
      footer={
        <Link className="font-medium text-primary underline-offset-4 hover:underline" to="/sign-in">
          Back to sign in
        </Link>
      }
    >
      <div className="space-y-4 text-sm">
        <MailCheck aria-hidden="true" className="size-8 text-primary" />
        <p>
          {address ? (
            <>
              We sent a verification link to <span className="font-medium break-all">{address}</span>.
            </>
          ) : (
            'We sent you a verification link.'
          )}{' '}
          Open it to activate your account. The link expires in 24 hours.
        </p>
        <p className="text-muted-foreground">
          Didn&apos;t get it? Check your spam folder, or send a new link.
        </p>
        {address ? <ResendVerification email={address} /> : null}
      </div>
    </AuthLayout>
  )
}
