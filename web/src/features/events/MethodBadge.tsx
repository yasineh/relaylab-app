import { cn } from '@/lib/utils'

const tones: Record<string, string> = {
  GET: 'bg-sky-100 text-sky-900 dark:bg-sky-500/20 dark:text-sky-100',
  POST: 'bg-emerald-100 text-emerald-900 dark:bg-emerald-500/20 dark:text-emerald-100',
  PUT: 'bg-amber-100 text-amber-900 dark:bg-amber-500/20 dark:text-amber-100',
  PATCH: 'bg-violet-100 text-violet-900 dark:bg-violet-500/20 dark:text-violet-100',
  DELETE: 'bg-rose-100 text-rose-900 dark:bg-rose-500/20 dark:text-rose-100',
}

/** The HTTP method as text (colour is only a secondary cue). */
export function MethodBadge({ method }: { method: string }) {
  return (
    <span
      className={cn(
        'inline-block min-w-16 rounded px-1.5 py-0.5 text-center font-mono text-xs font-semibold',
        tones[method] ?? 'bg-muted text-foreground',
      )}
    >
      {method}
    </span>
  )
}
