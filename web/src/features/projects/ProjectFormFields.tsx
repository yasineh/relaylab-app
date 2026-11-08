import type { FieldErrors, UseFormRegister } from 'react-hook-form'

import { FormField } from '@/components/form-field'
import { TextareaField } from '@/components/textarea-field'
import { DESCRIPTION_MAX, NAME_MAX } from '@/features/projects/api'
import type { ProjectFormValues } from '@/features/projects/schema'

export function ProjectFormFields({
  register,
  errors,
  autoFocusName,
}: {
  register: UseFormRegister<ProjectFormValues>
  errors: FieldErrors<ProjectFormValues>
  autoFocusName?: boolean
}) {
  return (
    <>
      <FormField
        label="Project name"
        autoComplete="off"
        hint={`Up to ${NAME_MAX} characters, for example "Stripe sandbox".`}
        error={errors.name?.message}
        {...(autoFocusName ? { 'data-autofocus': true } : {})}
        {...register('name')}
      />
      <TextareaField
        label="Description (optional)"
        rows={3}
        hint={`Up to ${DESCRIPTION_MAX} characters.`}
        error={errors.description?.message}
        {...register('description')}
      />
    </>
  )
}
