import { zodResolver } from '@hookform/resolvers/zod'
import { useQueryClient } from '@tanstack/react-query'
import { Loader2 } from 'lucide-react'
import { useState } from 'react'
import { useForm } from 'react-hook-form'
import { Link, Navigate, useNavigate } from 'react-router'

import { AuthLayout } from '@/components/auth-layout'
import { FormField } from '@/components/form-field'
import { Alert } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { mapAuthError } from '@/features/auth/errors'
import { PASSWORD_MAX, PASSWORD_MIN, signUpSchema, type SignUpValues } from '@/features/auth/schemas'
import { meQueryKey, useMe, usePublicConfig } from '@/hooks/session'
import { authClient } from '@/lib/auth-client'
import { DEFAULT_AFTER_SIGN_IN } from '@/lib/redirect'
import { NotFoundPage } from '@/pages/NotFoundPage'

export function SignUpPage() {
  const config = usePublicConfig()
  const me = useMe()
  if (me.data) return <Navigate to={DEFAULT_AFTER_SIGN_IN} replace />
  if (config.isPending) {
    return (
      <p className="flex items-center justify-center gap-2 py-16 text-muted-foreground">
        <Loader2 aria-hidden="true" className="size-4 animate-spin" /> Loading…
      </p>
    )
  }
  // When sign-up is disabled (or its state is unknown) the route does not exist.
  if (!config.data?.signup_enabled) return <NotFoundPage />
  return <SignUpForm verificationRequired={config.data.email_verification_required} />
}

function SignUpForm({ verificationRequired }: { verificationRequired: boolean }) {
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const [formError, setFormError] = useState<string | null>(null)
  const form = useForm<SignUpValues>({
    resolver: zodResolver(signUpSchema),
    defaultValues: { name: '', email: '', password: '' },
  })
  const { errors, isSubmitting } = form.formState

  const onSubmit = form.handleSubmit(async (values) => {
    setFormError(null)
    const { error } = await authClient.signUp.email({
      name: values.name.trim(),
      email: values.email,
      password: values.password,
      callbackURL: '/verify-email',
    })
    if (error) {
      const mapped = mapAuthError(error)
      if (mapped.field === 'password' || mapped.field === 'name' || mapped.field === 'email') {
        form.setError(mapped.field, { message: mapped.message }, { shouldFocus: true })
      } else {
        setFormError(mapped.message)
      }
      return
    }
    if (verificationRequired) {
      // The same screen for new and existing emails: sign-up never reveals whether an account exists.
      void navigate('/check-email', { state: { email: values.email } })
      return
    }
    await queryClient.invalidateQueries({ queryKey: meQueryKey })
    void navigate(DEFAULT_AFTER_SIGN_IN, { replace: true })
  })

  return (
    <AuthLayout
      title="Create your account"
      description="Start receiving and inspecting webhooks in minutes."
      footer={
        <>
          Already have an account?{' '}
          <Link className="font-medium text-primary underline-offset-4 hover:underline" to="/sign-in">
            Sign in
          </Link>
        </>
      }
    >
      <form noValidate onSubmit={onSubmit} className="space-y-4" aria-label="Create account">
        {formError ? <Alert tone="error">{formError}</Alert> : null}
        <FormField label="Name" autoComplete="name" error={errors.name?.message} {...form.register('name')} />
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
          autoComplete="new-password"
          hint={`${PASSWORD_MIN}–${PASSWORD_MAX} characters. Avoid common passwords.`}
          error={errors.password?.message}
          {...form.register('password')}
        />
        <Button type="submit" className="w-full" disabled={isSubmitting}>
          {isSubmitting ? <Loader2 aria-hidden="true" className="animate-spin" /> : null}
          {isSubmitting ? 'Creating account…' : 'Create account'}
        </Button>
      </form>
    </AuthLayout>
  )
}
