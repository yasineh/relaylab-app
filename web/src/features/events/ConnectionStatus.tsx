import type { StreamState } from '@/features/events/stream'
import { cn } from '@/lib/utils'

const text: Record<StreamState, { label: string; hint: string; dot: string }> = {
  connecting: {
    label: 'Connecting…',
    hint: 'Connecting to live updates.',
    dot: 'bg-muted-foreground',
  },
  live: {
    label: 'Live',
    hint: 'New events appear here as soon as they are received.',
    dot: 'bg-success',
  },
  reconnecting: {
    label: 'Reconnecting…',
    hint: 'The live connection dropped. Missed events load when it reconnects.',
    dot: 'bg-warning animate-pulse motion-reduce:animate-none',
  },
  offline: {
    label: 'Offline',
    hint: 'RelayLab can’t be reached. Still retrying; missed events load when it reconnects.',
    dot: 'bg-destructive',
  },
}

/**
 * The live connection state, as text plus a coloured dot (never colour
 * alone). Changes are announced politely to screen readers.
 */
export function ConnectionStatus({ state, className }: { state: StreamState; className?: string }) {
  const t = text[state]
  return (
    <p
      role="status"
      aria-live="polite"
      title={t.hint}
      className={cn('inline-flex items-center gap-1.5 text-xs font-medium text-muted-foreground', className)}
    >
      <span aria-hidden="true" className={cn('size-2 shrink-0 rounded-full', t.dot)} />
      <span>{t.label}</span>
      <span className="sr-only">{t.hint}</span>
    </p>
  )
}
