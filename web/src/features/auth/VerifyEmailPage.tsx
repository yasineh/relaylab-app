import { zodResolver } from '@hookform/resolvers/zod'
import { CircleCheck, Loader2 } from 'lucide-react'
import { useState } from 'react'
import { useForm } from 'react-hook-form'
import { Link, useSearchParams } from 'react-router'

import { AuthLayout } from '@/components/auth-layout'
import { FormField } from '@/components/form-field'
import { Alert } from '@/components/ui/alert'
import { Button, buttonVariants } from '@/components/ui/button'
import { mapAuthError } from '@/features/auth/errors'
import { forgotPasswordSchema, type ForgotPasswordValues } from '@/features/auth/schemas'
import { useMe } from '@/hooks/session'
import { authClient } from '@/lib/auth-client'
import { DEFAULT_AFTER_SIGN_IN } from '@/lib/redirect'

/**
 * Landing page for the emailed verification link. Better Auth verifies the token
 * at /api/auth/verify-email and redirects here, signed in on success or with
 * ?error=INVALID_TOKEN|TOKEN_EXPIRED on failure.
 */
export function VerifyEmailPage() {
  const [params] = useSearchParams()
  const error = params.get('error')
  if (error) return <VerificationFailed code={error} />
  return <VerificationSucceeded />
}

function VerificationSucceeded() {
  const me = useMe()
  if (me.isPending) {
    return (
      <p className="flex items-center justify-center gap-2 py-16 text-muted-foreground">
        <Loader2 aria-hidden="true" className="size-4 animate-spin" /> Confirming…
      </p>
    )
  }
  const signedIn = Boolean(me.data)
  return (
    <AuthLayout title="Email verified">
      <div className="space-y-4 text-sm">
        <p className="flex items-center gap-2 text-success">
          <CircleCheck aria-hidden="true" className="size-5" /> Your email address is confirmed.
        </p>
        <Link
          className={buttonVariants({ className: 'w-full' })}
          to={signedIn ? DEFAULT_AFTER_SIGN_IN : '/sign-in'}
        >
          {signedIn ? 'Continue to RelayLab' : 'Sign in'}
        </Link>
      </div>
    </AuthLayout>
  )
}

function VerificationFailed({ code }: { code: string }) {
  const message =
    code === 'TOKEN_EXPIRED'
      ? mapAuthError({ code }).message
      : mapAuthError({ code: 'INVALID_TOKEN' }).message
  const [sent, setSent] = useState(false)
  const [sendError, setSendError] = useState<string | null>(null)
  const form = useForm<ForgotPasswordValues>({
    resolver: zodResolver(forgotPasswordSchema),
    defaultValues: { email: '' },
  })
  const onSubmit = form.handleSubmit(async ({ email }) => {
    setSendError(null)
    const { error } = await authClient.sendVerificationEmail({ email, callbackURL: '/verify-email' })
    if (error) setSendError(mapAuthError(error).message)
    else setSent(true)
  })
  return (
    <AuthLayout
      title="We couldn't verify your email"
      footer={
        <Link className="font-medium text-primary underline-offset-4 hover:underline" to="/sign-in">
          Back to sign in
        </Link>
      }
    >
      <div className="space-y-4">
        <Alert tone="error">
          <p>{message} Your account is not verified yet.</p>
        </Alert>
        {sent ? (
          <Alert tone="success">If the account needs verifying, a new link is on its way.</Alert>
        ) : (
          <form
            noValidate
            onSubmit={onSubmit}
            className="space-y-4"
            aria-label="Send a new verification link"
          >
            {sendError ? <Alert tone="error">{sendError}</Alert> : null}
            <FormField
              label="Email"
              type="email"
              autoComplete="email"
              error={form.formState.errors.email?.message}
              {...form.register('email')}
            />
            <Button type="submit" className="w-full" disabled={form.formState.isSubmitting}>
              {form.formState.isSubmitting ? 'Sending…' : 'Send a new link'}
            </Button>
          </form>
        )}
      </div>
    </AuthLayout>
  )
}
