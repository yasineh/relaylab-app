import { cn } from '@/lib/utils'

export function EnabledBadge({ enabled }: { enabled: boolean }) {
  return (
    <span
      className={cn(
        'inline-flex items-center gap-1.5 rounded-full border px-2 py-0.5 text-xs font-medium',
        enabled ? 'border-success/40 text-success' : 'text-muted-foreground',
      )}
    >
      <span
        aria-hidden="true"
        className={cn('size-1.5 rounded-full', enabled ? 'bg-success' : 'bg-muted-foreground')}
      />
      {enabled ? 'Enabled' : 'Disabled'}
    </span>
  )
}
