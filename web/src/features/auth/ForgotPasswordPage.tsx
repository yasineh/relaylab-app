import { zodResolver } from '@hookform/resolvers/zod'
import { Loader2 } from 'lucide-react'
import { useState } from 'react'
import { useForm } from 'react-hook-form'
import { Link } from 'react-router'

import { AuthLayout } from '@/components/auth-layout'
import { FormField } from '@/components/form-field'
import { Alert } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { mapAuthError } from '@/features/auth/errors'
import { forgotPasswordSchema, type ForgotPasswordValues } from '@/features/auth/schemas'
import { authClient } from '@/lib/auth-client'

export function ForgotPasswordPage() {
  const [sent, setSent] = useState(false)
  const [formError, setFormError] = useState<string | null>(null)
  const form = useForm<ForgotPasswordValues>({
    resolver: zodResolver(forgotPasswordSchema),
    defaultValues: { email: '' },
  })
  const { errors, isSubmitting } = form.formState

  const onSubmit = form.handleSubmit(async ({ email }) => {
    setFormError(null)
    const { error } = await authClient.requestPasswordReset({ email, redirectTo: '/reset-password' })
    // The server answers identically for known and unknown emails; only transport
    // or rate-limit failures are errors.
    if (error) {
      setFormError(mapAuthError(error).message)
      return
    }
    setSent(true)
  })

  return (
    <AuthLayout
      title="Reset your password"
      description="Enter your account email and we'll send you a link to choose a new password."
      footer={
        <Link className="font-medium text-primary underline-offset-4 hover:underline" to="/sign-in">
          Back to sign in
        </Link>
      }
    >
      {sent ? (
        <Alert tone="success">
          If an account exists for that email, a reset link is on its way. The link expires in 1 hour.
        </Alert>
      ) : (
        <form noValidate onSubmit={onSubmit} className="space-y-4" aria-label="Request a password reset">
          {formError ? <Alert tone="error">{formError}</Alert> : null}
          <FormField
            label="Email"
            type="email"
            autoComplete="email"
            error={errors.email?.message}
            {...form.register('email')}
          />
          <Button type="submit" className="w-full" disabled={isSubmitting}>
            {isSubmitting ? <Loader2 aria-hidden="true" className="animate-spin" /> : null}
            {isSubmitting ? 'Sending…' : 'Send reset link'}
          </Button>
        </form>
      )}
    </AuthLayout>
  )
}
