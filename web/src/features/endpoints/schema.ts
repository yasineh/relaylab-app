import type { UseFormSetError } from 'react-hook-form'
import { z } from 'zod'

import { ENDPOINT_NAME_MAX } from '@/features/endpoints/api'
import { isApiError } from '@/lib/api'

// Mirrors the server rules (trimmed, 1–100 characters, no control characters);
// the server stays authoritative and returns 422 field errors.
export const endpointNameSchema = z.object({
  name: z
    .string()
    .trim()
    .min(1, 'Enter an endpoint name.')
    .max(ENDPOINT_NAME_MAX, `Use at most ${ENDPOINT_NAME_MAX} characters.`)
    .refine((v) => !/\p{Cc}/u.test(v), 'Name must not contain control characters.'),
})

export type EndpointNameValues = z.infer<typeof endpointNameSchema>

/**
 * Applies a failed create or rename to a form: a 422 name error goes next to the
 * input; anything else becomes a form-level message (returned).
 */
export function applyEndpointError(
  err: unknown,
  setError: UseFormSetError<EndpointNameValues>,
): string | null {
  if (isApiError(err) && err.status === 422 && err.fields?.name) {
    setError('name', { message: err.fields.name }, { shouldFocus: true })
    return null
  }
  if (isApiError(err) && err.code === 'endpoint_limit_reached') {
    return 'This project has reached its endpoint limit. Delete an endpoint to create another.'
  }
  if (isApiError(err) && err.status === 404) {
    return 'This endpoint no longer exists.'
  }
  return 'Something went wrong. Try again.'
}
