import type { ComponentProps } from 'react'

import { cn } from '@/lib/utils'

export function Label({ className, ...props }: ComponentProps<'label'>) {
  // oxlint-disable-next-line jsx-a11y/label-has-associated-control -- callers pass htmlFor
  return <label className={cn('text-sm font-medium leading-none', className)} {...props} />
}
