import type { UseFormSetError } from 'react-hook-form'

import type { ProjectFormValues } from '@/features/projects/schema'
import { isApiError } from '@/lib/api'

/**
 * Applies a failed project request to a form: 422 field errors go next to their
 * inputs; anything else becomes a form-level message (returned).
 */
export function applyProjectError(err: unknown, setError: UseFormSetError<ProjectFormValues>): string | null {
  if (isApiError(err) && err.status === 422 && err.fields) {
    let applied = false
    for (const field of ['name', 'description'] as const) {
      const message = err.fields[field]
      if (message) {
        setError(field, { message }, { shouldFocus: !applied })
        applied = true
      }
    }
    if (applied) return null
  }
  if (isApiError(err) && err.code === 'project_limit_reached') {
    return 'You have reached the maximum number of projects. Delete one to create another.'
  }
  return 'Something went wrong. Try again.'
}
