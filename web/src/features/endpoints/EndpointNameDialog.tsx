import { zodResolver } from '@hookform/resolvers/zod'
import { Loader2 } from 'lucide-react'
import { useState } from 'react'
import { useForm } from 'react-hook-form'

import { FormField } from '@/components/form-field'
import { Alert } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Dialog } from '@/components/ui/dialog'
import { ENDPOINT_NAME_MAX } from '@/features/endpoints/api'
import { applyEndpointError, endpointNameSchema, type EndpointNameValues } from '@/features/endpoints/schema'

/**
 * The create and rename dialogs: one name field validated like the server.
 * `onSubmit` performs the request; a rejection is shown in the dialog.
 */
export function EndpointNameDialog({
  open,
  onClose,
  title,
  description,
  submitLabel,
  pendingLabel,
  initialName = '',
  onSubmit,
}: {
  open: boolean
  onClose: () => void
  title: string
  description: string
  submitLabel: string
  pendingLabel: string
  initialName?: string
  onSubmit: (name: string) => Promise<void>
}) {
  const [formError, setFormError] = useState<string | null>(null)
  const form = useForm<EndpointNameValues>({
    resolver: zodResolver(endpointNameSchema),
    values: { name: initialName },
  })
  const { errors, isSubmitting } = form.formState

  function close() {
    form.reset({ name: initialName })
    setFormError(null)
    onClose()
  }

  const submit = form.handleSubmit(async (values) => {
    setFormError(null)
    try {
      await onSubmit(values.name.trim())
      form.reset({ name: initialName })
    } catch (err) {
      setFormError(applyEndpointError(err, form.setError))
    }
  })

  return (
    <Dialog open={open} onClose={close} title={title} description={description}>
      <form noValidate onSubmit={submit} className="space-y-4" aria-label={title}>
        {formError ? <Alert tone="error">{formError}</Alert> : null}
        <FormField
          label="Endpoint name"
          autoComplete="off"
          hint={`Up to ${ENDPOINT_NAME_MAX} characters, for example "Stripe staging".`}
          error={errors.name?.message}
          data-autofocus
          {...form.register('name')}
        />
        <div className="flex justify-end gap-2">
          <Button variant="ghost" onClick={close}>
            Cancel
          </Button>
          <Button type="submit" disabled={isSubmitting}>
            {isSubmitting ? <Loader2 aria-hidden="true" className="animate-spin" /> : null}
            {isSubmitting ? pendingLabel : submitLabel}
          </Button>
        </div>
      </form>
    </Dialog>
  )
}
