import { Activity, Loader2 } from 'lucide-react'

import { Alert } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { VisibleText } from '@/features/events/payload/RawText'
import { useAuditLog, type AuditEntry } from '@/features/replay/api'
import { isApiError } from '@/lib/api'
import { formatPreciseDateTime } from '@/lib/format'

const ACTIONS: Record<string, string> = {
  'replay.created': 'Replay created',
  'endpoint.created': 'Endpoint created',
  'endpoint.updated': 'Endpoint updated',
  'endpoint.rotated': 'Endpoint URL rotated',
  'endpoint.deleted': 'Endpoint deleted',
}

const actionLabel = (a: string) => ACTIONS[a] ?? a

function resource(e: AuditEntry): string {
  const m = e.metadata
  if (e.action === 'replay.created') {
    const to = m.destination_type === 'sink' ? 'test sink' : (m.destination_host ?? 'URL')
    return `${m.method ?? ''} to ${to}`.trim()
  }
  return m.name ?? e.resource_type
}

/** The project's audit log (read-only). */
export function ActivityPanel({ projectId }: { projectId: string }) {
  const log = useAuditLog(projectId)
  if (log.isPending) {
    return (
      <output className="flex items-center gap-2 text-sm text-muted-foreground">
        <Loader2 aria-hidden="true" className="size-4 animate-spin" /> Loading activity…
      </output>
    )
  }
  if (!log.data) {
    if (isApiError(log.error) && log.error.status === 401) return null
    return (
      <Alert tone="error">
        <p>Activity could not be loaded.</p>
        <Button variant="outline" size="sm" onClick={() => void log.refetch()}>
          Try again
        </Button>
      </Alert>
    )
  }
  const items = log.data.pages.flatMap((p) => p.items)
  if (items.length === 0) {
    return (
      <section className="flex max-w-xl flex-col items-start gap-2 rounded-lg border border-dashed bg-card p-6">
        <Activity aria-hidden="true" className="size-6 text-primary" />
        <h3 className="font-medium">No activity yet</h3>
        <p className="text-sm text-muted-foreground">
          Changes to endpoints and replays you start are recorded here.
        </p>
      </section>
    )
  }
  return (
    <div className="space-y-3">
      <ul aria-label="Project activity" className="divide-y rounded-lg border bg-card text-sm">
        {items.map((e) => (
          <li key={e.id} className="flex flex-wrap items-baseline gap-x-3 gap-y-1 px-4 py-3">
            <span className="font-medium">{actionLabel(e.action)}</span>
            <span className="text-muted-foreground">
              <VisibleText text={resource(e)} />
            </span>
            <span className="ml-auto text-xs text-muted-foreground">
              {e.actor_name ?? 'Deleted user'} ·{' '}
              <time dateTime={e.created_at}>{formatPreciseDateTime(e.created_at)}</time>
            </span>
          </li>
        ))}
      </ul>
      {log.hasNextPage ? (
        <Button variant="outline" onClick={() => void log.fetchNextPage()} disabled={log.isFetchingNextPage}>
          {log.isFetchingNextPage ? 'Loading more…' : 'Load more'}
        </Button>
      ) : null}
    </div>
  )
}
