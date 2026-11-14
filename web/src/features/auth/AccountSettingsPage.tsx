import { zodResolver } from '@hookform/resolvers/zod'
import { useQueryClient } from '@tanstack/react-query'
import { Loader2 } from 'lucide-react'
import { useId, useState, type ReactNode } from 'react'
import { useForm } from 'react-hook-form'
import { useNavigate } from 'react-router'
import { z } from 'zod'

import { FormField } from '@/components/form-field'
import { Alert } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { mapAuthError } from '@/features/auth/errors'
import { PASSWORD_MAX, PASSWORD_MIN, nameSchema, newPasswordSchema } from '@/features/auth/schemas'
import { leaveSession, meQueryKey, useMe } from '@/hooks/session'
import { authClient } from '@/lib/auth-client'

export function AccountSettingsPage() {
  const me = useMe()
  return (
    <div className="max-w-2xl space-y-8">
      <div className="space-y-1">
        <h1 className="text-2xl font-semibold tracking-tight">Account settings</h1>
        <p className="text-muted-foreground">
          Signed in as <span className="font-medium text-foreground break-all">{me.data?.email}</span>
        </p>
      </div>
      <ProfileSection initialName={me.data?.name ?? ''} />
      <PasswordSection />
      <SessionsSection />
      <DeleteAccountSection />
    </div>
  )
}

function Section({
  title,
  description,
  children,
  tone,
}: {
  title: string
  description: string
  children: ReactNode
  tone?: 'danger'
}) {
  const headingId = useId()
  return (
    <section
      aria-labelledby={headingId}
      className={`rounded-lg border bg-card p-6 ${tone === 'danger' ? 'border-destructive/40' : ''}`}
    >
      <h2 id={headingId} className="font-semibold">
        {title}
      </h2>
      <p className="mt-1 mb-4 text-sm text-muted-foreground">{description}</p>
      {children}
    </section>
  )
}

type Status = { tone: 'success' | 'error'; message: string } | null

const profileSchema = z.object({ name: nameSchema })

function ProfileSection({ initialName }: { initialName: string }) {
  const queryClient = useQueryClient()
  const [status, setStatus] = useState<Status>(null)
  const form = useForm<z.infer<typeof profileSchema>>({
    resolver: zodResolver(profileSchema),
    values: { name: initialName },
  })
  const { errors, isSubmitting } = form.formState
  const onSubmit = form.handleSubmit(async ({ name }) => {
    setStatus(null)
    const { error } = await authClient.updateUser({ name: name.trim() })
    if (error) {
      const mapped = mapAuthError(error)
      if (mapped.field === 'name') form.setError('name', { message: mapped.message }, { shouldFocus: true })
      else setStatus({ tone: 'error', message: mapped.message })
      return
    }
    await queryClient.invalidateQueries({ queryKey: meQueryKey })
    setStatus({ tone: 'success', message: 'Your display name was updated.' })
  })
  return (
    <Section
      title="Profile"
      description="Your name is shown in RelayLab. It is never shared with webhook senders."
    >
      <form noValidate onSubmit={onSubmit} className="space-y-4" aria-label="Profile">
        {status ? <Alert tone={status.tone}>{status.message}</Alert> : null}
        <FormField
          label="Display name"
          autoComplete="name"
          error={errors.name?.message}
          {...form.register('name')}
        />
        <Button type="submit" disabled={isSubmitting}>
          {isSubmitting ? <Loader2 aria-hidden="true" className="animate-spin" /> : null}
          {isSubmitting ? 'Saving…' : 'Save name'}
        </Button>
      </form>
    </Section>
  )
}

const passwordSchema = z
  .object({
    currentPassword: z.string().min(1, 'Enter your current password.'),
    newPassword: newPasswordSchema,
    confirm: z.string(),
  })
  .refine((v) => v.newPassword === v.confirm, { path: ['confirm'], message: 'Passwords do not match.' })

function PasswordSection() {
  const [status, setStatus] = useState<Status>(null)
  const form = useForm<z.infer<typeof passwordSchema>>({
    resolver: zodResolver(passwordSchema),
    defaultValues: { currentPassword: '', newPassword: '', confirm: '' },
  })
  const { errors, isSubmitting } = form.formState
  const onSubmit = form.handleSubmit(async ({ currentPassword, newPassword }) => {
    setStatus(null)
    const { error } = await authClient.changePassword({
      currentPassword,
      newPassword,
      revokeOtherSessions: true,
    })
    if (error) {
      const mapped = mapAuthError(error)
      if (mapped.field === 'currentPassword') {
        form.setError('currentPassword', { message: mapped.message }, { shouldFocus: true })
      } else if (mapped.field === 'password') {
        form.setError('newPassword', { message: mapped.message }, { shouldFocus: true })
      } else {
        setStatus({ tone: 'error', message: mapped.message })
      }
      return
    }
    form.reset()
    setStatus({ tone: 'success', message: 'Password changed. You were signed out on every other device.' })
  })
  return (
    <Section title="Password" description="Changing your password signs you out on every other device.">
      <form noValidate onSubmit={onSubmit} className="space-y-4" aria-label="Change password">
        {status ? <Alert tone={status.tone}>{status.message}</Alert> : null}
        <FormField
          label="Current password"
          type="password"
          autoComplete="current-password"
          error={errors.currentPassword?.message}
          {...form.register('currentPassword')}
        />
        <FormField
          label="New password"
          type="password"
          autoComplete="new-password"
          hint={`${PASSWORD_MIN}–${PASSWORD_MAX} characters. Avoid common passwords.`}
          error={errors.newPassword?.message}
          {...form.register('newPassword')}
        />
        <FormField
          label="Confirm new password"
          type="password"
          autoComplete="new-password"
          error={errors.confirm?.message}
          {...form.register('confirm')}
        />
        <Button type="submit" disabled={isSubmitting}>
          {isSubmitting ? <Loader2 aria-hidden="true" className="animate-spin" /> : null}
          {isSubmitting ? 'Changing…' : 'Change password'}
        </Button>
      </form>
    </Section>
  )
}

function SessionsSection() {
  const queryClient = useQueryClient()
  const navigate = useNavigate()
  const [pending, setPending] = useState(false)
  const [error, setError] = useState<string | null>(null)
  async function signOutEverywhere() {
    setPending(true)
    setError(null)
    const { error: err } = await authClient.revokeSessions()
    if (err) {
      setPending(false)
      setError(mapAuthError(err).message)
      return
    }
    await leaveSession(queryClient, navigate, '/sign-in?reason=signed-out')
  }
  return (
    <Section
      title="Sessions"
      description="Sign out of RelayLab on every device and browser, including this one."
    >
      <div className="space-y-3">
        {error ? <Alert tone="error">{error}</Alert> : null}
        <Button variant="outline" onClick={() => void signOutEverywhere()} disabled={pending}>
          {pending ? <Loader2 aria-hidden="true" className="animate-spin" /> : null}
          {pending ? 'Signing out…' : 'Sign out everywhere'}
        </Button>
      </div>
    </Section>
  )
}

const deleteSchema = z.object({
  password: z.string().min(1, 'Enter your password to confirm.'),
  confirmed: z.boolean().refine((v) => v, { message: 'Confirm that you understand this cannot be undone.' }),
})

function DeleteAccountSection() {
  const queryClient = useQueryClient()
  const navigate = useNavigate()
  const [open, setOpen] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const checkboxId = useId()
  const form = useForm<{ password: string; confirmed: boolean }>({
    resolver: zodResolver(deleteSchema),
    defaultValues: { password: '', confirmed: false },
  })
  const { errors, isSubmitting } = form.formState
  const onSubmit = form.handleSubmit(async ({ password }) => {
    setError(null)
    const { error: err } = await authClient.deleteUser({ password })
    if (err) {
      const mapped = mapAuthError(err)
      if (mapped.code === 'INVALID_PASSWORD') {
        form.setError('password', { message: 'The password is incorrect.' }, { shouldFocus: true })
      } else {
        setError(mapped.message)
      }
      return
    }
    await leaveSession(queryClient, navigate, '/sign-in?reason=account-deleted')
  })
  return (
    <Section
      tone="danger"
      title="Delete account"
      description="Permanently deletes your account and everything it owns, including all projects and their events, and signs you out everywhere. This cannot be undone."
    >
      {!open ? (
        <Button variant="destructive" onClick={() => setOpen(true)}>
          Delete account…
        </Button>
      ) : (
        <form noValidate onSubmit={onSubmit} className="space-y-4" aria-label="Delete account">
          {error ? <Alert tone="error">{error}</Alert> : null}
          <FormField
            label="Password"
            type="password"
            autoComplete="current-password"
            error={errors.password?.message}
            {...form.register('password')}
          />
          <div className="space-y-1">
            <div className="flex items-start gap-2">
              <input
                id={checkboxId}
                type="checkbox"
                className="mt-0.5 size-4 accent-destructive"
                aria-invalid={errors.confirmed ? true : undefined}
                aria-describedby={errors.confirmed ? `${checkboxId}-error` : undefined}
                {...form.register('confirmed')}
              />
              <label htmlFor={checkboxId} className="text-sm">
                I understand that my account, projects, and events will be permanently deleted.
              </label>
            </div>
            {errors.confirmed ? (
              <p id={`${checkboxId}-error`} className="text-xs font-medium text-destructive">
                {errors.confirmed.message}
              </p>
            ) : null}
          </div>
          <div className="flex gap-2">
            <Button type="submit" variant="destructive" disabled={isSubmitting}>
              {isSubmitting ? <Loader2 aria-hidden="true" className="animate-spin" /> : null}
              {isSubmitting ? 'Deleting…' : 'Permanently delete account'}
            </Button>
            <Button
              variant="ghost"
              onClick={() => {
                form.reset()
                setError(null)
                setOpen(false)
              }}
            >
              Cancel
            </Button>
          </div>
        </form>
      )}
    </Section>
  )
}
