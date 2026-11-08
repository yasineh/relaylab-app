import { z } from 'zod'

import { DESCRIPTION_MAX, NAME_MAX } from '@/features/projects/api'

// Mirrors the server rules; the server stays authoritative (422 field errors).
export const projectSchema = z.object({
  name: z
    .string()
    .trim()
    .min(1, 'Enter a project name.')
    .max(NAME_MAX, `Use at most ${NAME_MAX} characters.`),
  description: z.string().trim().max(DESCRIPTION_MAX, `Use at most ${DESCRIPTION_MAX} characters.`),
})

export type ProjectFormValues = z.infer<typeof projectSchema>

/** Form values to the API body: an empty description is sent as null. */
export const toInput = (v: ProjectFormValues) => ({
  name: v.name.trim(),
  description: v.description.trim() === '' ? null : v.description.trim(),
})
