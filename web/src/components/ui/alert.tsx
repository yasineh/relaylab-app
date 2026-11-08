import { CircleAlert, CircleCheck, Info } from 'lucide-react'
import type { ReactNode } from 'react'

import { cn } from '@/lib/utils'

type Tone = 'error' | 'success' | 'info'

const styles: Record<Tone, string> = {
  error: 'border-destructive/40 bg-destructive/5 text-destructive',
  success: 'border-success/40 bg-success/5 text-success',
  info: 'border-primary/30 bg-accent text-accent-foreground',
}
const icons = { error: CircleAlert, success: CircleCheck, info: Info }

/**
 * Inline status message. Errors use role="alert" so screen readers announce
 * them immediately; other tones use role="status".
 */
export function Alert({
  tone = 'info',
  children,
  className,
}: {
  tone?: Tone
  children: ReactNode
  className?: string
}) {
  const Icon = icons[tone]
  return (
    <div
      role={tone === 'error' ? 'alert' : 'status'}
      className={cn('flex gap-2 rounded-md border px-3 py-2.5 text-sm', styles[tone], className)}
    >
      <Icon aria-hidden="true" className="mt-0.5 size-4 shrink-0" />
      <div className="space-y-1">{children}</div>
    </div>
  )
}
