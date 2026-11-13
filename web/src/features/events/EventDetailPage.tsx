import { FileQuestion, Info, Loader2, Trash2 } from 'lucide-react'
import { useId, useMemo, useState } from 'react'
import { Link, useNavigate, useParams, useSearchParams } from 'react-router'

import { CopyButton } from '@/components/copy-button'
import { Alert } from '@/components/ui/alert'
import { Button, buttonVariants } from '@/components/ui/button'
import { Dialog } from '@/components/ui/dialog'
import { Tabs } from '@/components/ui/tabs'
import { useEndpoint } from '@/features/endpoints/api'
import { eventBodyPath, useDeleteEvent, useEvent, type Entry, type EventDetail } from '@/features/events/api'
import { headersText, queryText, rawRequestText, requestTarget } from '@/features/events/copy-text'
import { buildCurl } from '@/features/events/curl'
import { MethodBadge } from '@/features/events/MethodBadge'
import { base64ToBytes, formatBytes } from '@/features/events/payload/bytes'
import { HexView } from '@/features/events/payload/HexView'
import { parseJson } from '@/features/events/payload/json-rows'
import { JsonTree } from '@/features/events/payload/JsonTree'
import { RawText, VisibleText } from '@/features/events/payload/RawText'
import { REDACTION_NOTE, RedactedMarker } from '@/features/events/RedactedMarker'
import { useCurrentProject } from '@/features/projects/ProjectLayout'
import { EventReplayPanel } from '@/features/replay/EventReplayPanel'
import { isApiError } from '@/lib/api'
import { formatPreciseDateTime } from '@/lib/format'

const TABS = ['overview', 'body', 'headers', 'query', 'raw'] as const
type TabId = (typeof TABS)[number]

export function EventDetailPage() {
  const project = useCurrentProject()
  const { eventId = '' } = useParams()
  const event = useEvent(project.id, eventId)

  if (event.isPending) {
    return (
      <output className="flex items-center gap-2 text-muted-foreground">
        <Loader2 aria-hidden="true" className="size-4 animate-spin" /> Loading event…
      </output>
    )
  }
  if (event.isError) {
    if (isApiError(event.error) && event.error.status === 404) return <EventNotFound projectId={project.id} />
    if (isApiError(event.error) && event.error.status === 401) return null // global handler redirects
    return (
      <Alert tone="error">
        <p>This event could not be loaded.</p>
        <Button variant="outline" size="sm" onClick={() => void event.refetch()}>
          Try again
        </Button>
      </Alert>
    )
  }
  return <EventView projectId={project.id} event={event.data} />
}

function EventView({ projectId, event: e }: { projectId: string; event: EventDetail }) {
  const [params, setParams] = useSearchParams()
  const tab = (TABS as readonly string[]).includes(params.get('tab') ?? '')
    ? (params.get('tab') as TabId)
    : 'overview'
  const select = (id: string) => {
    const next = new URLSearchParams(params)
    if (id === 'overview') next.delete('tab')
    else next.set('tab', id)
    setParams(next, { replace: true })
  }
  const noteId = useId()

  return (
    <div className="space-y-6">
      <div className="space-y-2">
        <p className="text-sm text-muted-foreground">
          <Link to={`/projects/${projectId}/events`} className="underline-offset-4 hover:underline">
            Events
          </Link>
        </p>
        <div className="flex flex-wrap items-center gap-3">
          <MethodBadge method={e.method} />
          <h2 className="min-w-0 flex-1 font-mono text-lg font-semibold break-all">
            <VisibleText text={requestTarget(e)} />
          </h2>
          <DeleteEvent projectId={projectId} eventId={e.id} />
        </div>
        <p className="text-sm text-muted-foreground">
          Received <time dateTime={e.received_at}>{formatPreciseDateTime(e.received_at)}</time> by{' '}
          <Link
            to={`/projects/${projectId}/endpoints/${e.endpoint_id}`}
            className="font-medium text-foreground underline-offset-4 hover:underline"
          >
            {e.endpoint_name}
          </Link>
        </p>
      </div>

      <ReproduceCurl projectId={projectId} event={e} />

      <EventReplayPanel projectId={projectId} event={e} />

      <Alert tone="info">
        <p>
          Webhook payloads may contain confidential or personal data. Treat this page like the system that
          sent it.
          {e.redacted_count > 0
            ? ` ${e.redacted_count} credential ${e.redacted_count === 1 ? 'value was' : 'values were'} removed when the request arrived.`
            : ''}
        </p>
      </Alert>
      <p id={noteId} className="sr-only">
        {REDACTION_NOTE}
      </p>

      <Tabs
        label="Event details"
        selected={tab}
        onSelect={select}
        tabs={[
          { id: 'overview', label: 'Overview', panel: <Overview event={e} /> },
          { id: 'body', label: 'Body', panel: <BodyPanel projectId={projectId} event={e} /> },
          {
            id: 'headers',
            label: `Headers (${e.headers.length})`,
            panel: <EntriesPanel kind="headers" entries={e.headers} noteId={noteId} />,
          },
          {
            id: 'query',
            label: `Query (${e.query.length})`,
            panel: <EntriesPanel kind="query" entries={e.query} noteId={noteId} rawQuery={e.raw_query} />,
          },
          { id: 'raw', label: 'Raw request', panel: <RawRequest event={e} /> },
        ]}
      />
    </div>
  )
}

function DeleteEvent({ projectId, eventId }: { projectId: string; eventId: string }) {
  const del = useDeleteEvent(projectId, eventId)
  const navigate = useNavigate()
  const [open, setOpen] = useState(false)
  const [error, setError] = useState<string | null>(null)

  function close() {
    setError(null)
    setOpen(false)
  }

  async function confirm() {
    setError(null)
    try {
      await del.mutateAsync()
      void navigate(`/projects/${projectId}/events`, { replace: true })
    } catch (err) {
      if (isApiError(err) && err.status === 404) {
        void navigate(`/projects/${projectId}/events`, { replace: true }) // already gone
        return
      }
      setError('The event could not be deleted. Try again.')
    }
  }

  return (
    <>
      <Button variant="outline" size="sm" onClick={() => setOpen(true)}>
        <Trash2 aria-hidden="true" /> Delete event
      </Button>
      <Dialog
        open={open}
        onClose={close}
        title="Delete this event?"
        description="This permanently deletes the event and its replay history. It cannot be undone."
      >
        <div className="space-y-4">
          {error ? <Alert tone="error">{error}</Alert> : null}
          <div className="flex justify-end gap-2">
            <Button variant="ghost" onClick={close} data-autofocus>
              Cancel
            </Button>
            <Button variant="destructive" onClick={() => void confirm()} disabled={del.isPending}>
              {del.isPending ? <Loader2 aria-hidden="true" className="animate-spin" /> : null}
              {del.isPending ? 'Deleting…' : 'Delete event'}
            </Button>
          </div>
        </div>
      </Dialog>
    </>
  )
}

function ReproduceCurl({ projectId, event: e }: { projectId: string; event: EventDetail }) {
  const endpoint = useEndpoint(projectId, e.endpoint_id)
  if (endpoint.isPending) return null
  const url = endpoint.data?.url
  if (!url) {
    return (
      <p className="text-sm text-muted-foreground">
        A curl command can&apos;t be built because this endpoint&apos;s receiving URL is unavailable.
      </p>
    )
  }
  const { command, notes } = buildCurl(e, url)
  return (
    <div className="space-y-1">
      <CopyButton value={command} label="Copy as curl" />
      {notes.map((n) => (
        <p key={n} className="text-xs text-muted-foreground">
          {n}
        </p>
      ))}
    </div>
  )
}

function Overview({ event: e }: { event: EventDetail }) {
  const rows: [string, string][] = [
    ['Received', formatPreciseDateTime(e.received_at)],
    ['Endpoint', e.endpoint_name],
    ['Method', e.method],
    ['Path', e.path || '/'],
    ['Content type', e.content_type ?? 'None'],
    ['Body size', formatBytes(e.body.size)],
    ['Removed values', String(e.redacted_count)],
    ['Source network', e.source_ip_prefix ?? 'Unknown'],
    ['User agent', e.user_agent ?? 'None'],
    ['Request ID', e.correlation_id],
    ['Event ID', e.id],
  ]
  return (
    <dl className="grid gap-x-6 gap-y-3 rounded-lg border bg-card p-4 text-sm sm:grid-cols-[12rem_1fr]">
      {rows.map(([k, v]) => (
        <div key={k} className="contents">
          <dt className="text-muted-foreground">{k}</dt>
          <dd className="font-mono break-all">
            <VisibleText text={v} />
          </dd>
        </div>
      ))}
      <dt className="sr-only">Note</dt>
      <dd className="text-xs text-muted-foreground sm:col-span-2">
        Source network is the sender&apos;s address truncated to a /24 (IPv4) or /48 (IPv6) network.
      </dd>
    </dl>
  )
}

function BodyPanel({ projectId, event: e }: { projectId: string; event: EventDetail }) {
  const download = eventBodyPath(projectId, e.id)
  if (e.body.encoding === 'binary') {
    return (
      <div className="space-y-3">
        <p className="text-sm text-muted-foreground">
          This body is not valid UTF-8 text, so it is shown as bytes.
        </p>
        <HexView bytes={base64ToBytes(e.body.base64 ?? '')} downloadHref={download} />
      </div>
    )
  }
  return (
    <TextBody
      text={e.body.text ?? ''}
      isJson={e.body.is_json}
      jsonInvalid={e.body.json_invalid}
      download={download}
    />
  )
}

function TextBody({
  text,
  isJson,
  jsonInvalid,
  download,
}: {
  text: string
  isJson: boolean
  jsonInvalid: boolean
  download: string
}) {
  const parsed = useMemo(() => (isJson ? parseJson(text) : ({ ok: false } as const)), [isJson, text])
  const [view, setView] = useState<'tree' | 'raw'>(parsed.ok ? 'tree' : 'raw')
  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center gap-2">
        {parsed.ok ? (
          <fieldset className="inline-flex rounded-md border p-0.5">
            <legend className="sr-only">Body view</legend>
            {(['tree', 'raw'] as const).map((v) => (
              <Button
                key={v}
                size="sm"
                variant={view === v ? 'secondary' : 'ghost'}
                aria-pressed={view === v}
                onClick={() => setView(v)}
              >
                {v === 'tree' ? 'Tree' : 'Raw'}
              </Button>
            ))}
          </fieldset>
        ) : null}
        <CopyButton value={text} label="Copy body" />
        <a href={download} download className={buttonVariants({ variant: 'ghost', size: 'sm' })}>
          Download raw
        </a>
      </div>
      {jsonInvalid ? (
        <Alert tone="info">
          <p>The content type says JSON, but the body is not valid JSON. It is shown as received.</p>
        </Alert>
      ) : null}
      {parsed.ok && view === 'tree' ? <JsonTree value={parsed.value} /> : <RawText text={text} />}
    </div>
  )
}

function EntriesPanel({
  kind,
  entries,
  noteId,
  rawQuery,
}: {
  kind: 'headers' | 'query'
  entries: Entry[]
  noteId: string
  rawQuery?: string
}) {
  const noun = kind === 'headers' ? 'header' : 'query parameter'
  if (entries.length === 0) {
    return <p className="text-sm text-muted-foreground">This request had no {noun}s.</p>
  }
  const removed = entries.some((x) => x.redacted)
  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center gap-2">
        <CopyButton
          value={kind === 'headers' ? headersText(entries) : queryText(entries)}
          label={`Copy ${noun}s`}
        />
        {removed ? (
          <span className="text-xs text-muted-foreground">
            Copied text contains [REDACTED] for removed values.
          </span>
        ) : null}
      </div>
      {removed ? (
        <p className="flex items-start gap-2 text-sm text-muted-foreground">
          <Info aria-hidden="true" className="mt-0.5 size-4 shrink-0" />
          {REDACTION_NOTE}
        </p>
      ) : null}
      {rawQuery ? (
        <p className="text-sm">
          <span className="text-muted-foreground">Raw query: </span>
          <code className="font-mono break-all">
            <VisibleText text={rawQuery} />
          </code>
        </p>
      ) : null}
      <div className="overflow-x-auto rounded-lg border bg-card">
        <table className="w-full text-sm">
          <caption className="sr-only">{kind === 'headers' ? 'Request headers' : 'Query parameters'}</caption>
          <thead className="border-b text-left text-xs text-muted-foreground">
            <tr>
              <th scope="col" className="px-4 py-2.5 font-medium">
                Name
              </th>
              <th scope="col" className="px-4 py-2.5 font-medium">
                Value
              </th>
            </tr>
          </thead>
          <tbody className="divide-y font-mono">
            {entries.map((x, i) => (
              <tr key={`${x.name}-${i}`}>
                <th scope="row" className="px-4 py-2 text-left align-top font-medium break-all">
                  <VisibleText text={x.name} />
                </th>
                <td className="px-4 py-2 break-all whitespace-pre-wrap">
                  {x.redacted ? <RedactedMarker describedBy={noteId} /> : <VisibleText text={x.value} />}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  )
}

function RawRequest({ event: e }: { event: EventDetail }) {
  const text = rawRequestText(e)
  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center gap-2">
        <CopyButton value={text} label="Copy raw request" />
        <span className="text-xs text-muted-foreground">
          The request line is relative to the endpoint&apos;s receiving URL.
        </span>
      </div>
      <RawText text={text} label="Raw request text" />
    </div>
  )
}

function EventNotFound({ projectId }: { projectId: string }) {
  return (
    <section className="mx-auto flex max-w-md flex-col items-center gap-3 py-16 text-center">
      <FileQuestion aria-hidden="true" className="size-8 text-muted-foreground" />
      <h2 className="text-xl font-semibold">Event not found</h2>
      <p className="text-sm text-muted-foreground">It may have been deleted, or the link is wrong.</p>
      <Link to={`/projects/${projectId}/events`} className={buttonVariants({ variant: 'outline' })}>
        Back to events
      </Link>
    </section>
  )
}
