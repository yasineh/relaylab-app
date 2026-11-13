import { History, Loader2, SearchX } from 'lucide-react'
import { useState } from 'react'
import { Link, useSearchParams } from 'react-router'

import { Alert } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Dialog } from '@/components/ui/dialog'
import { Label } from '@/components/ui/label'
import { formatBytes } from '@/features/events/payload/bytes'
import { VisibleText } from '@/features/events/payload/RawText'
import { useCurrentProject } from '@/features/projects/ProjectLayout'
import {
  categoryLabel,
  useReplay,
  useReplays,
  type Replay,
  type ReplayFilters,
  type ReplayState,
} from '@/features/replay/api'
import { ReplayStateBadge } from '@/features/replay/ReplayStateBadge'
import { isApiError } from '@/lib/api'
import { formatPreciseDateTime } from '@/lib/format'

const STATES: ReplayState[] = ['queued', 'running', 'succeeded', 'failed']

export function ReplayHistoryPage() {
  const project = useCurrentProject()
  const [params, setParams] = useSearchParams()
  const state = STATES.find((s) => s === params.get('state'))
  const eventId = params.get('event_id') ?? undefined
  const filters: ReplayFilters = { state, event_id: eventId }
  const replays = useReplays(project.id, filters)
  const selected = params.get('replay')

  const set = (key: string, value: string | null) => {
    const next = new URLSearchParams(params)
    if (value) next.set(key, value)
    else next.delete(key)
    setParams(next)
  }
  const items = replays.data?.pages.flatMap((p) => p.items) ?? []
  const filtered = Boolean(state || eventId)

  return (
    <div className="space-y-6">
      <p className="max-w-2xl text-muted-foreground">
        Every replay of this project&apos;s events, newest first. Responses are stored with each replay and
        may contain data from the destination.
      </p>
      <div className="flex flex-wrap items-end gap-4">
        <div className="space-y-1.5">
          <Label htmlFor="replay-state">State</Label>
          <select
            id="replay-state"
            value={state ?? ''}
            onChange={(e) => set('state', e.target.value || null)}
            className="h-9 w-44 rounded-md border bg-background px-3 text-sm"
          >
            <option value="">All states</option>
            {STATES.map((s) => (
              <option key={s} value={s}>
                {s[0]!.toUpperCase() + s.slice(1)}
              </option>
            ))}
          </select>
        </div>
        {eventId ? (
          <p className="text-sm">
            Showing replays of one event.{' '}
            <Button variant="outline" size="sm" onClick={() => set('event_id', null)}>
              Show all
            </Button>
          </p>
        ) : null}
      </div>

      {replays.isPending ? (
        <output className="flex items-center gap-2 text-muted-foreground">
          <Loader2 aria-hidden="true" className="size-4 animate-spin" /> Loading replays…
        </output>
      ) : !replays.data ? (
        isApiError(replays.error) && replays.error.status === 401 ? null : (
          <Alert tone="error">
            <p>Replays could not be loaded.</p>
            <Button variant="outline" size="sm" onClick={() => void replays.refetch()}>
              Try again
            </Button>
          </Alert>
        )
      ) : items.length === 0 ? (
        filtered ? (
          <section className="flex max-w-xl flex-col items-start gap-3 rounded-lg border border-dashed bg-card p-6">
            <SearchX aria-hidden="true" className="size-6 text-muted-foreground" />
            <h2 className="font-medium">No replays match</h2>
            <Button variant="outline" onClick={() => setParams(new URLSearchParams())}>
              Clear filters
            </Button>
          </section>
        ) : (
          <section className="flex max-w-xl flex-col items-start gap-3 rounded-lg border border-dashed bg-card p-6">
            <History aria-hidden="true" className="size-6 text-primary" />
            <h2 className="font-medium">No replays yet</h2>
            <p className="text-sm text-muted-foreground">
              Open an event and choose <span className="font-medium text-foreground">Replay</span> to send it
              again to a test sink or a URL you control. Its result appears here.
            </p>
            <Link to={`/projects/${project.id}/events`} className="text-sm underline underline-offset-4">
              Go to events
            </Link>
          </section>
        )
      ) : (
        <>
          <ReplayTable items={items} onOpen={(id) => set('replay', id)} />
          {replays.hasNextPage ? (
            <Button
              variant="outline"
              onClick={() => void replays.fetchNextPage()}
              disabled={replays.isFetchingNextPage}
            >
              {replays.isFetchingNextPage ? 'Loading more…' : 'Load more'}
            </Button>
          ) : (
            <p className="text-sm text-muted-foreground">No more replays.</p>
          )}
        </>
      )}

      <ReplayDetailDialog projectId={project.id} replayId={selected} onClose={() => set('replay', null)} />
    </div>
  )
}

function ReplayTable({ items, onOpen }: { items: Replay[]; onOpen: (id: string) => void }) {
  return (
    <div className="overflow-x-auto rounded-lg border bg-card">
      <table className="w-full text-sm">
        <caption className="sr-only">Replays</caption>
        <thead className="border-b text-left text-xs text-muted-foreground">
          <tr>
            <th scope="col" className="px-4 py-2.5 font-medium">
              Created
            </th>
            <th scope="col" className="px-4 py-2.5 font-medium">
              Target
            </th>
            <th scope="col" className="px-4 py-2.5 font-medium">
              State
            </th>
            <th scope="col" className="px-4 py-2.5 text-right font-medium">
              Duration
            </th>
            <th scope="col" className="px-4 py-2.5">
              <span className="sr-only">Details</span>
            </th>
          </tr>
        </thead>
        <tbody className="divide-y">
          {items.map((r) => (
            <tr key={r.id}>
              <td className="px-4 py-3 whitespace-nowrap">
                <time dateTime={r.created_at}>{formatPreciseDateTime(r.created_at)}</time>
              </td>
              <th scope="row" className="max-w-md px-4 py-3 text-left font-mono font-normal break-all">
                <VisibleText text={`${r.method} ${r.target}`} />
              </th>
              <td className="px-4 py-3">
                <ReplayStateBadge replay={r} />
              </td>
              <td className="px-4 py-3 text-right whitespace-nowrap text-muted-foreground">
                {r.duration_ms !== null ? `${r.duration_ms} ms` : '—'}
              </td>
              <td className="px-4 py-3 text-right">
                <Button variant="outline" size="sm" onClick={() => onOpen(r.id)}>
                  View<span className="sr-only"> replay {formatPreciseDateTime(r.created_at)}</span>
                </Button>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

function ReplayDetailDialog({
  projectId,
  replayId,
  onClose,
}: {
  projectId: string
  replayId: string | null
  onClose: () => void
}) {
  const replay = useReplay(projectId, replayId)
  return (
    <Dialog open={replayId !== null} onClose={onClose} title="Replay details" size="lg">
      {replay.isPending ? (
        <output className="flex items-center gap-2 text-sm text-muted-foreground">
          <Loader2 aria-hidden="true" className="size-4 animate-spin" /> Loading replay…
        </output>
      ) : replay.isError ? (
        <p className="text-sm text-destructive">
          {isApiError(replay.error) && replay.error.status === 404
            ? 'This replay was not found. It may have been deleted with its event.'
            : 'This replay could not be loaded.'}
        </p>
      ) : (
        <ReplayDetail projectId={projectId} replay={replay.data} />
      )}
    </Dialog>
  )
}

function ReplayDetail({ projectId, replay: r }: { projectId: string; replay: Replay }) {
  const [showAll, setShowAll] = useState(false)
  const rows: [string, React.ReactNode][] = [
    ['State', <ReplayStateBadge key="s" replay={r} />],
    ['Target', <VisibleText key="t" text={r.target} />],
    ['Method', r.method],
    ['Created', formatPreciseDateTime(r.created_at)],
    ['Started', r.started_at ? formatPreciseDateTime(r.started_at) : '—'],
    ['Finished', r.finished_at ? formatPreciseDateTime(r.finished_at) : '—'],
    ['Duration', r.duration_ms !== null ? `${r.duration_ms} ms` : '—'],
    ['Tries', String(r.tries)],
  ]
  const body = r.response?.body
  const text = body?.encoding === 'utf8' ? (body.text ?? '') : null
  return (
    <div className="space-y-4 text-sm">
      <dl className="grid gap-x-6 gap-y-2 sm:grid-cols-[8rem_1fr]">
        {rows.map(([k, v]) => (
          <div key={k} className="contents">
            <dt className="text-muted-foreground">{k}</dt>
            <dd className="font-mono break-all">{v}</dd>
          </div>
        ))}
      </dl>
      {r.state === 'failed' ? (
        <Alert tone="error">
          <p>
            <span className="font-medium">{categoryLabel(r.failure_category)}.</span> {r.failure_message}
          </p>
        </Alert>
      ) : null}
      {r.header_overrides && r.header_overrides.length > 0 ? (
        <p>
          <span className="text-muted-foreground">Added headers: </span>
          <span className="font-mono">
            {r.header_overrides.map((h) => `${h.name}: ${h.value}`).join(', ')}
          </span>
        </p>
      ) : null}
      {r.response ? (
        <section aria-labelledby="response-heading" className="space-y-2">
          <h3 id="response-heading" className="font-semibold">
            Response {r.response.status}
          </h3>
          {r.response.headers.length > 0 ? (
            <table className="w-full text-xs">
              <caption className="sr-only">Response headers</caption>
              <tbody>
                {r.response.headers.map((h, i) => (
                  <tr key={`${h.name}-${i}`} className="border-t">
                    <th scope="row" className="py-1 pr-3 text-left font-mono font-normal">
                      <VisibleText text={h.name} />
                    </th>
                    <td className="py-1 font-mono break-all">
                      <VisibleText text={h.value} />
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          ) : null}
          <p className="text-xs text-muted-foreground">
            Body: {formatBytes(body?.size ?? 0)}
            {body?.truncated ? ' (truncated: only the beginning is stored)' : ''}
          </p>
          {text !== null ? (
            <pre
              aria-label="Response body"
              className="max-h-80 overflow-auto rounded-md border bg-muted/40 p-3 font-mono text-xs break-all whitespace-pre-wrap"
            >
              <VisibleText text={showAll || text.length <= 4000 ? text : text.slice(0, 4000)} />
            </pre>
          ) : (
            <p className="text-xs text-muted-foreground">The body is binary and is not shown.</p>
          )}
          {text !== null && text.length > 4000 && !showAll ? (
            <Button variant="outline" size="sm" onClick={() => setShowAll(true)}>
              Show the whole excerpt
            </Button>
          ) : null}
        </section>
      ) : null}
      <Link
        to={`/projects/${projectId}/events/${r.event_id}`}
        className="inline-block underline underline-offset-4"
      >
        Open the original event
      </Link>
    </div>
  )
}
