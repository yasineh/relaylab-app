import { CheckCircle2, Clock, Loader2, XCircle } from 'lucide-react'

import { stateLabel, type Replay } from '@/features/replay/api'
import { cn } from '@/lib/utils'

/** The replay's state as text plus an icon (never colour alone). */
export function ReplayStateBadge({ replay }: { replay: Replay }) {
  const Icon = { queued: Clock, running: Loader2, succeeded: CheckCircle2, failed: XCircle }[replay.state]
  return (
    <span
      className={cn(
        'inline-flex items-center gap-1 rounded-md border px-2 py-0.5 text-xs font-medium whitespace-nowrap',
        replay.state === 'failed' && 'border-destructive/40 text-destructive',
        replay.state === 'succeeded' && 'border-success/40 text-success',
      )}
    >
      <Icon
        aria-hidden="true"
        className={cn('size-3.5', replay.state === 'running' && 'animate-spin motion-reduce:animate-none')}
      />
      {stateLabel(replay)}
    </span>
  )
}
