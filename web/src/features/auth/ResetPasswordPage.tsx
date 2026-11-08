import { zodResolver } from '@hookform/resolvers/zod'
import { useQueryClient } from '@tanstack/react-query'
import { Loader2 } from 'lucide-react'
import { useState } from 'react'
import { useForm } from 'react-hook-form'
import { Link, useNavigate, useSearchParams } from 'react-router'

import { AuthLayout } from '@/components/auth-layout'
import { FormField } from '@/components/form-field'
import { Alert } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { mapAuthError } from '@/features/auth/errors'
import {
  PASSWORD_MAX,
  PASSWORD_MIN,
  resetPasswordSchema,
  type ResetPasswordValues,
} from '@/features/auth/schemas'
import { authClient } from '@/lib/auth-client'

/**
 * Landing page for the emailed reset link. Better Auth checks the token at
 * /api/auth/reset-password/<token> and redirects here with ?token=… or
 * ?error=INVALID_TOKEN.
 */
export function ResetPasswordPage() {
  const [params] = useSearchParams()
  const token = params.get('token')
  const linkError = params.get('error')
  if (!token || linkError) return <InvalidResetLink />
  return <ResetPasswordForm token={token} />
}

function InvalidResetLink() {
  return (
    <AuthLayout title="This reset link can't be used">
      <div className="space-y-4">
        <Alert tone="error">
          <p>The link is invalid, has expired, or was already used. Reset links work once, for 1 hour.</p>
        </Alert>
        <Link
          className="block text-center text-sm font-medium text-primary underline-offset-4 hover:underline"
          to="/forgot-password"
        >
          Request a new link
        </Link>
      </div>
    </AuthLayout>
  )
}

function ResetPasswordForm({ token }: { token: string }) {
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const [formError, setFormError] = useState<string | null>(null)
  const form = useForm<ResetPasswordValues>({
    resolver: zodResolver(resetPasswordSchema),
    defaultValues: { password: '', confirm: '' },
  })
  const { errors, isSubmitting } = form.formState

  const onSubmit = form.handleSubmit(async ({ password }) => {
    setFormError(null)
    const { error } = await authClient.resetPassword({ newPassword: password, token })
    if (error) {
      const mapped = mapAuthError(error)
      if (mapped.field === 'password')
        form.setError('password', { message: mapped.message }, { shouldFocus: true })
      else setFormError(mapped.code === 'UNKNOWN' ? 'This link is invalid or has expired.' : mapped.message)
      return
    }
    // Resetting signs the user out everywhere.
    queryClient.clear()
    void navigate('/sign-in?reason=password-reset', { replace: true })
  })

  return (
    <AuthLayout
      title="Choose a new password"
      description="Resetting your password signs you out on every device."
    >
      <form noValidate onSubmit={onSubmit} className="space-y-4" aria-label="Choose a new password">
        {formError ? <Alert tone="error">{formError}</Alert> : null}
        <FormField
          label="New password"
          type="password"
          autoComplete="new-password"
          hint={`${PASSWORD_MIN}–${PASSWORD_MAX} characters. Avoid common passwords.`}
          error={errors.password?.message}
          {...form.register('password')}
        />
        <FormField
          label="Confirm new password"
          type="password"
          autoComplete="new-password"
          error={errors.confirm?.message}
          {...form.register('confirm')}
        />
        <Button type="submit" className="w-full" disabled={isSubmitting}>
          {isSubmitting ? <Loader2 aria-hidden="true" className="animate-spin" /> : null}
          {isSubmitting ? 'Saving…' : 'Set new password'}
        </Button>
      </form>
    </AuthLayout>
  )
}
