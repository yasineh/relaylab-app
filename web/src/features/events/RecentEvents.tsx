import { useQueryClient } from '@tanstack/react-query'
import { Inbox, Loader2 } from 'lucide-react'
import { Link } from 'react-router'

import { Button, buttonVariants } from '@/components/ui/button'
import { useEvents, type EventFilters } from '@/features/events/api'
import { ConnectionStatus } from '@/features/events/ConnectionStatus'
import { useEventStream, useLiveInsert } from '@/features/events/live'
import { MethodBadge } from '@/features/events/MethodBadge'
import { VisibleText } from '@/features/events/payload/RawText'
import { isApiError } from '@/lib/api'
import { useNow } from '@/hooks/now'
import { formatPreciseDateTime, formatRelative } from '@/lib/format'

/** How many events the overview card lists. */
export const RECENT_COUNT = 5

// The overview shares the unfiltered, newest-first list cache with the events page.
const ALL: EventFilters = {}

/** The project overview's newest events, updated live. */
export function RecentEvents({ projectId }: { projectId: string }) {
  const queryClient = useQueryClient()
  const events = useEvents(projectId, ALL)
  const now = useNow()
  const insert = useLiveInsert(projectId, ALL)
  const state = useEventStream(projectId, {
    // A small card has no scroll position to protect: always insert.
    onEvent: (e) => void insert([e]),
    onSync: () => void queryClient.invalidateQueries({ queryKey: ['events', projectId] }),
  })
  const items = events.data?.pages[0]?.items.slice(0, RECENT_COUNT) ?? []
  const allEvents = `/projects/${projectId}/events`

  return (
    <section aria-labelledby="events-heading" className="rounded-lg border bg-card p-6">
      <div className="mb-3 flex items-center justify-between gap-3">
        <h2 id="events-heading" className="flex items-center gap-2 font-medium">
          <Inbox aria-hidden="true" className="size-5 text-primary" /> Recent events
        </h2>
        <ConnectionStatus state={state} />
      </div>
      {events.isPending ? (
        <output className="flex items-center gap-2 text-sm text-muted-foreground">
          <Loader2 aria-hidden="true" className="size-4 animate-spin" /> Loading events…
        </output>
      ) : events.isError && !events.data ? (
        isApiError(events.error) && events.error.status === 401 ? null : (
          <div className="space-y-2 text-sm">
            <p className="text-destructive">Recent events could not be loaded.</p>
            <Button variant="outline" size="sm" onClick={() => void events.refetch()}>
              Try again
            </Button>
          </div>
        )
      ) : items.length === 0 ? (
        <p className="text-sm text-muted-foreground">
          No events yet. Send a request to one of this project’s endpoints and it appears here as soon as it
          arrives.
        </p>
      ) : (
        <>
          <ul aria-label="Recent events" className="divide-y text-sm">
            {items.map((e) => (
              <li key={e.id} className="flex items-center gap-3 py-2">
                <MethodBadge method={e.method} />
                <Link
                  to={`/projects/${projectId}/events/${e.id}`}
                  className="min-w-0 flex-1 truncate font-mono underline-offset-4 hover:underline"
                >
                  <VisibleText text={e.path || '/'} />
                </Link>
                <time
                  dateTime={e.received_at}
                  title={formatPreciseDateTime(e.received_at)}
                  className="shrink-0 text-xs text-muted-foreground"
                >
                  {formatRelative(e.received_at, now)}
                </time>
              </li>
            ))}
          </ul>
          <Link
            to={allEvents}
            className={buttonVariants({ variant: 'outline', size: 'sm', className: 'mt-3' })}
          >
            View all events
          </Link>
        </>
      )}
    </section>
  )
}
