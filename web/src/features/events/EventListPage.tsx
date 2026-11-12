import { ArrowUp, Inbox, Loader2, SearchX } from 'lucide-react'
import { useState } from 'react'
import { Link, useSearchParams } from 'react-router'

import { CopyButton } from '@/components/copy-button'
import { Alert } from '@/components/ui/alert'
import { Button, buttonVariants } from '@/components/ui/button'
import { curlExample, useEndpoints, type Endpoint } from '@/features/endpoints/api'
import {
  filtersFromParams,
  filtersToParams,
  hasFilters,
  useEvents,
  type EventFilters,
  type EventSummary,
} from '@/features/events/api'
import { EventFilterBar } from '@/features/events/EventFilterBar'
import { useLiveEventList } from '@/features/events/live'
import { MethodBadge } from '@/features/events/MethodBadge'
import { formatBytes } from '@/features/events/payload/bytes'
import { VisibleText } from '@/features/events/payload/RawText'
import { useCurrentProject } from '@/features/projects/ProjectLayout'
import { isApiError } from '@/lib/api'
import { formatPreciseDateTime, formatRelative } from '@/lib/format'

export function EventListPage() {
  const project = useCurrentProject()
  const [params, setParams] = useSearchParams()
  const filters = filtersFromParams(params)
  const endpoints = useEndpoints(project.id)
  const events = useEvents(project.id, filters)
  const live = useLiveEventList(project.id, filters)

  const apply = (f: EventFilters) => setParams(filtersToParams(f))
  const clear = () => setParams(new URLSearchParams())
  const validation =
    isApiError(events.error) && events.error.code === 'validation_failed' ? events.error.fields : undefined

  return (
    <div className="space-y-6">
      <p className="max-w-2xl text-muted-foreground">
        Every request your endpoints received, newest first. Payloads may contain confidential or personal
        data.
      </p>
      <EventFilterBar
        // Remount when the URL changes (back/forward, clear) so the form shows the applied filters.
        key={params.toString()}
        filters={filters}
        endpoints={endpoints.data ?? []}
        fieldErrors={validation}
        onApply={apply}
        onClear={clear}
      />
      {live.hidden > 0 ? (
        <p className="flex flex-wrap items-center gap-x-2 text-sm text-muted-foreground">
          {countLabel(live.hidden)} {live.hidden === 1 ? 'doesn’t' : 'don’t'} match these filters.
          <Button variant="outline" size="sm" onClick={clear}>
            Clear filters
          </Button>
        </p>
      ) : null}
      {live.pending > 0 ? (
        <div className="sticky top-4 z-10 flex justify-center">
          <Button size="sm" className="rounded-full shadow-md" onClick={live.showPending}>
            <ArrowUp aria-hidden="true" /> Show {countLabel(live.pending)}
          </Button>
        </div>
      ) : null}

      {events.isPending ? (
        <output className="flex items-center gap-2 text-muted-foreground">
          <Loader2 aria-hidden="true" className="size-4 animate-spin" /> Loading events…
        </output>
      ) : !events.data ? (
        <ListError
          error={events.error}
          validation={Boolean(validation)}
          onRetry={() => void events.refetch()}
          onClear={clear}
        />
      ) : events.data.pages[0]?.items.length === 0 ? (
        hasFilters(filters) ? (
          <NoResults onClear={clear} />
        ) : (
          <NoEvents projectId={project.id} endpoints={endpoints.data} />
        )
      ) : (
        <>
          <EventTable projectId={project.id} events={events.data.pages.flatMap((p) => p.items)} />
          {events.isFetchNextPageError ? (
            // A failed "Load more" keeps the loaded rows (TanStack keeps data on error).
            <Alert tone="error">
              <p>More events could not be loaded.</p>
              <Button variant="outline" size="sm" onClick={() => void events.fetchNextPage()}>
                Try again
              </Button>
            </Alert>
          ) : events.hasNextPage ? (
            <Button
              variant="outline"
              onClick={() => void events.fetchNextPage()}
              disabled={events.isFetchingNextPage}
            >
              {events.isFetchingNextPage ? (
                <>
                  <Loader2 aria-hidden="true" className="animate-spin" /> Loading more…
                </>
              ) : (
                'Load more'
              )}
            </Button>
          ) : (
            <p className="text-sm text-muted-foreground">No more events.</p>
          )}
        </>
      )}
    </div>
  )
}

const countLabel = (n: number) => `${n} new ${n === 1 ? 'event' : 'events'}`

function ListError({
  error,
  validation,
  onRetry,
  onClear,
}: {
  error: Error | null
  validation: boolean
  onRetry: () => void
  onClear: () => void
}) {
  if (isApiError(error) && error.status === 401) return null // global handler redirects
  if (validation) {
    return (
      <Alert tone="error">
        <p>Some filters are invalid. Fix the highlighted fields, or clear the filters.</p>
        <Button variant="outline" size="sm" onClick={onClear}>
          Clear filters
        </Button>
      </Alert>
    )
  }
  if (isApiError(error) && error.code === 'invalid_cursor') {
    return (
      <Alert tone="error">
        <p>These results changed while you were paging. Start again from the newest events.</p>
        <Button variant="outline" size="sm" onClick={onRetry}>
          Reload
        </Button>
      </Alert>
    )
  }
  return (
    <Alert tone="error">
      <p>The events could not be loaded.</p>
      <Button variant="outline" size="sm" onClick={onRetry}>
        Try again
      </Button>
    </Alert>
  )
}

function NoResults({ onClear }: { onClear: () => void }) {
  return (
    <section className="flex max-w-xl flex-col items-start gap-3 rounded-lg border border-dashed bg-card p-6">
      <SearchX aria-hidden="true" className="size-6 text-muted-foreground" />
      <h2 className="font-medium">No events match these filters</h2>
      <p className="text-sm text-muted-foreground">Try a wider time range or fewer filters.</p>
      <Button variant="outline" onClick={onClear}>
        Clear filters
      </Button>
    </section>
  )
}

function NoEvents({ projectId, endpoints }: { projectId: string; endpoints: Endpoint[] | undefined }) {
  const target = endpoints?.find((e) => e.enabled && e.url)
  return (
    <section className="flex max-w-2xl flex-col items-start gap-3 rounded-lg border border-dashed bg-card p-6">
      <Inbox aria-hidden="true" className="size-6 text-primary" />
      <h2 className="font-medium">No events yet</h2>
      {target?.url ? (
        <>
          <p className="text-sm text-muted-foreground">
            Send a test request to <span className="font-medium text-foreground">{target.name}</span>, or
            point a webhook provider at its receiving URL. It appears here as soon as it arrives.
          </p>
          <pre className="w-full overflow-x-auto rounded-md border bg-muted/40 p-3 font-mono text-xs break-all whitespace-pre-wrap">
            {curlExample(target.url)}
          </pre>
          <CopyButton value={curlExample(target.url)} label="Copy curl command" />
        </>
      ) : (
        <>
          <p className="text-sm text-muted-foreground">
            Create an endpoint to get a receiving URL, then send a request to it.
          </p>
          <Link to={`/projects/${projectId}/endpoints`} className={buttonVariants({ variant: 'outline' })}>
            Go to endpoints
          </Link>
        </>
      )}
    </section>
  )
}

function EventTable({ projectId, events }: { projectId: string; events: EventSummary[] }) {
  // Relative times are computed against the moment the table mounted.
  const [now] = useState(() => new Date())
  return (
    <div className="overflow-x-auto rounded-lg border bg-card">
      <table className="w-full text-sm">
        <caption className="sr-only">Received events</caption>
        <thead className="border-b text-left text-xs text-muted-foreground">
          <tr>
            <th scope="col" className="px-4 py-2.5 font-medium">
              Method
            </th>
            <th scope="col" className="px-4 py-2.5 font-medium">
              Path
            </th>
            <th scope="col" className="px-4 py-2.5 font-medium">
              Endpoint
            </th>
            <th scope="col" className="px-4 py-2.5 text-right font-medium">
              Size
            </th>
            <th scope="col" className="px-4 py-2.5 font-medium">
              Received
            </th>
          </tr>
        </thead>
        <tbody className="divide-y">
          {events.map((e) => (
            <tr key={e.id} className="hover:bg-muted/40">
              <td className="px-4 py-3">
                <MethodBadge method={e.method} />
              </td>
              <th scope="row" className="max-w-md px-4 py-3 text-left font-normal">
                <Link
                  to={`/projects/${projectId}/events/${e.id}`}
                  className="font-mono break-all underline-offset-4 hover:underline"
                >
                  <VisibleText text={e.path || '/'} />
                </Link>
                {e.content_type ? (
                  <span className="mt-0.5 block truncate text-xs text-muted-foreground">
                    <VisibleText text={e.content_type} />
                  </span>
                ) : null}
              </th>
              <td className="max-w-48 px-4 py-3 break-words text-muted-foreground">{e.endpoint_name}</td>
              <td className="px-4 py-3 text-right whitespace-nowrap text-muted-foreground">
                {formatBytes(e.body_size)}
              </td>
              <td className="px-4 py-3 whitespace-nowrap">
                <time dateTime={e.received_at} title={formatPreciseDateTime(e.received_at)}>
                  {formatRelative(e.received_at, now)}
                </time>
                <span className="block text-xs text-muted-foreground">
                  {formatPreciseDateTime(e.received_at)}
                </span>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}
