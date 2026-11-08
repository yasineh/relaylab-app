import { zodResolver } from '@hookform/resolvers/zod'
import { useQueryClient } from '@tanstack/react-query'
import { Loader2 } from 'lucide-react'
import { useState } from 'react'
import { useForm } from 'react-hook-form'
import { Link, Navigate, useNavigate, useSearchParams } from 'react-router'

import { AuthLayout } from '@/components/auth-layout'
import { FormField } from '@/components/form-field'
import { Alert } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { mapAuthError } from '@/features/auth/errors'
import { ResendVerification } from '@/features/auth/ResendVerification'
import { signInSchema, type SignInValues } from '@/features/auth/schemas'
import { meQueryKey, useMe, usePublicConfig } from '@/hooks/session'
import { authClient } from '@/lib/auth-client'
import { safeNext } from '@/lib/redirect'

const notices: Record<string, string> = {
  expired: 'Your session has expired. Sign in again to continue.',
  'password-reset': 'Your password was changed. Sign in with your new password.',
  'signed-out': 'You have been signed out.',
  'account-deleted': 'Your account has been deleted.',
}

export function SignInPage() {
  const [params] = useSearchParams()
  const next = safeNext(params.get('next'))
  const notice = notices[params.get('reason') ?? '']
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const me = useMe()
  const config = usePublicConfig()
  const [formError, setFormError] = useState<{ message: string; code: string } | null>(null)
  const [unverifiedEmail, setUnverifiedEmail] = useState<string | null>(null)

  const form = useForm<SignInValues>({
    resolver: zodResolver(signInSchema),
    defaultValues: { email: '', password: '' },
  })
  const { errors, isSubmitting } = form.formState

  if (me.data) return <Navigate to={next} replace />

  const onSubmit = form.handleSubmit(async (values) => {
    setFormError(null)
    setUnverifiedEmail(null)
    const { error } = await authClient.signIn.email({ email: values.email, password: values.password })
    if (error) {
      const mapped = mapAuthError(error)
      setFormError(mapped)
      if (mapped.code === 'EMAIL_NOT_VERIFIED') setUnverifiedEmail(values.email)
      form.setValue('password', '')
      return
    }
    await queryClient.invalidateQueries({ queryKey: meQueryKey })
    void navigate(next, { replace: true })
  })

  return (
    <AuthLayout
      title="Sign in to RelayLab"
      description="Receive, inspect, and replay webhooks."
      footer={
        config.data?.signup_enabled ? (
          <>
            New to RelayLab?{' '}
            <Link className="font-medium text-primary underline-offset-4 hover:underline" to="/sign-up">
              Create an account
            </Link>
          </>
        ) : null
      }
    >
      <form noValidate onSubmit={onSubmit} className="space-y-4" aria-label="Sign in">
        {notice && !formError ? <Alert tone="info">{notice}</Alert> : null}
        {formError ? (
          <Alert tone="error">
            <p>{formError.message}</p>
            {unverifiedEmail ? <ResendVerification email={unverifiedEmail} /> : null}
          </Alert>
        ) : null}
        <FormField
          label="Email"
          type="email"
          autoComplete="email"
          error={errors.email?.message}
          {...form.register('email')}
        />
        <FormField
          label="Password"
          type="password"
          autoComplete="current-password"
          error={errors.password?.message}
          {...form.register('password')}
        />
        <div className="flex justify-end">
          <Link to="/forgot-password" className="text-sm text-primary underline-offset-4 hover:underline">
            Forgot password?
          </Link>
        </div>
        <Button type="submit" className="w-full" disabled={isSubmitting}>
          {isSubmitting ? <Loader2 aria-hidden="true" className="animate-spin" /> : null}
          {isSubmitting ? 'Signing in…' : 'Sign in'}
        </Button>
      </form>
    </AuthLayout>
  )
}
