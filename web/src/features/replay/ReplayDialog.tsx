import { Info, Loader2, Plus, Trash2 } from 'lucide-react'
import { useEffect, useId, useMemo, useRef, useState } from 'react'
import { Link } from 'react-router'

import { Alert } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Dialog } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import type { Endpoint } from '@/features/endpoints/api'
import { formatBytes } from '@/features/events/payload/bytes'
import { VisibleText } from '@/features/events/payload/RawText'
import {
  REPLAY_METHODS,
  useCreateReplay,
  usePreviewReplay,
  type Preview,
  type Replay,
  type ReplayInput,
} from '@/features/replay/api'
import { isApiError } from '@/lib/api'

/** Shown whenever the dialog is open (event-replay spec). */
export const SIGNATURE_NOTICE =
  'A replay re-sends the stored body. It does not reproduce the original signature or authentication. Signature and credential headers are not forwarded unless you add them.'

const MAX_OVERRIDES = 20
const PREVIEW_DELAY_MS = 400

type Override = { id: number; name: string; value: string }

type Props = {
  open: boolean
  onClose: () => void
  onQueued: (r: Replay) => void
  projectId: string
  event: { id: string; method: string; endpoint_id: string }
  endpoints: Endpoint[]
  /** URL replay needs a worker with an active outbound guard. */
  urlAvailable: boolean
}

export function ReplayDialog({ open, onClose, onQueued, projectId, event, endpoints, urlAvailable }: Props) {
  // Remount per opening: fresh fields and a fresh Idempotency-Key.
  return open ? (
    <Dialog open onClose={onClose} title="Replay event" size="lg">
      <ReplayForm
        onClose={onClose}
        onQueued={onQueued}
        projectId={projectId}
        event={event}
        endpoints={endpoints}
        urlAvailable={urlAvailable}
      />
    </Dialog>
  ) : null
}

function ReplayForm({ onClose, onQueued, projectId, event, endpoints, urlAvailable }: Omit<Props, 'open'>) {
  const sinks = useMemo(
    () => endpoints.filter((e) => e.enabled && e.id !== event.endpoint_id),
    [endpoints, event.endpoint_id],
  )
  const [type, setType] = useState<'sink' | 'url'>(sinks.length > 0 || !urlAvailable ? 'sink' : 'url')
  const [sinkId, setSinkId] = useState(sinks[0]?.id ?? '')
  const [url, setUrl] = useState('')
  const [method, setMethod] = useState(
    (REPLAY_METHODS as readonly string[]).includes(event.method) ? event.method : 'POST',
  )
  const [overrides, setOverrides] = useState<Override[]>([])
  const nextId = useRef(1)
  const [idempotencyKey] = useState(() => crypto.randomUUID())
  const preview = usePreviewReplay(projectId, event.id)
  const create = useCreateReplay(projectId, event.id)
  const [shown, setShown] = useState<{ key: string; preview?: Preview; error?: unknown } | null>(null)
  const [sendError, setSendError] = useState<unknown>(null)
  const ids = {
    type: useId(),
    sink: useId(),
    url: useId(),
    method: useId(),
    urlErr: useId(),
    sinkErr: useId(),
    ovErr: useId(),
  }

  const input: ReplayInput | null = useMemo(() => {
    const header_overrides: Record<string, string> = {}
    for (const o of overrides) if (o.name.trim()) header_overrides[o.name.trim()] = o.value
    const base = { method, ...(overrides.length ? { header_overrides } : {}) }
    if (type === 'sink')
      return sinkId ? { ...base, destination: { type: 'sink', endpoint_id: sinkId } } : null
    return url.trim() ? { ...base, destination: { type: 'url', url: url.trim() } } : null
  }, [type, sinkId, url, method, overrides])
  const inputKey = input ? JSON.stringify(input) : ''

  // Debounced live preview of exactly what would be sent.
  const previewRef = useRef(preview.mutateAsync)
  useEffect(() => {
    previewRef.current = preview.mutateAsync
  })
  useEffect(() => {
    if (!input) return
    const timer = setTimeout(() => {
      previewRef.current(input).then(
        (p) => setShown({ key: inputKey, preview: p }),
        (error: unknown) => setShown({ key: inputKey, error }),
      )
    }, PREVIEW_DELAY_MS)
    return () => clearTimeout(timer)
  }, [input, inputKey])

  const current = shown && shown.key === inputKey ? shown : null
  const error = sendError ?? current?.error
  const fields = isApiError(error) ? (error.fields ?? {}) : {}
  const unavailable = isApiError(error) && error.code === 'replay_unavailable'
  const canSend = Boolean(input && current?.preview) && !create.isPending

  const send = async () => {
    if (!input) return
    setSendError(null)
    try {
      onQueued(await create.mutateAsync({ input, idempotencyKey }))
    } catch (err) {
      setSendError(err) // shown inline; the dialog stays open
    }
  }

  return (
    <form
      noValidate
      aria-label="Replay event"
      className="space-y-5"
      onSubmit={(e) => {
        e.preventDefault()
        void send()
      }}
    >
      <Alert tone="info">
        <p>{SIGNATURE_NOTICE}</p>
      </Alert>

      <fieldset className="space-y-3">
        <legend className="mb-1 text-sm font-medium">Destination</legend>
        <div className="flex flex-wrap gap-4">
          <label className="flex items-center gap-2 text-sm">
            <input type="radio" name={ids.type} checked={type === 'sink'} onChange={() => setType('sink')} />
            Test sink
          </label>
          <label className={`flex items-center gap-2 text-sm ${urlAvailable ? '' : 'text-muted-foreground'}`}>
            <input
              type="radio"
              name={ids.type}
              checked={type === 'url'}
              disabled={!urlAvailable}
              onChange={() => setType('url')}
            />
            URL
          </label>
        </div>
        {!urlAvailable ? (
          <p className="text-xs text-muted-foreground">
            Sending to a URL is unavailable right now: the outbound safety check has not reported in. The test
            sink still works.
          </p>
        ) : null}

        {type === 'sink' ? (
          sinks.length === 0 ? (
            <p className="text-sm text-muted-foreground">
              The test sink stores the replay as a new event on another endpoint of this project, without any
              network request. This project has no other enabled endpoint.{' '}
              <Link to={`/projects/${projectId}/endpoints`} className="underline underline-offset-4">
                Create one
              </Link>
              .
            </p>
          ) : (
            <div className="space-y-1.5">
              <Label htmlFor={ids.sink}>Sink endpoint</Label>
              <select
                id={ids.sink}
                value={sinkId}
                onChange={(e) => setSinkId(e.target.value)}
                aria-invalid={fields['destination.endpoint_id'] ? true : undefined}
                aria-describedby={fields['destination.endpoint_id'] ? ids.sinkErr : undefined}
                className="h-9 w-full rounded-md border bg-background px-3 text-sm"
              >
                {sinks.map((s) => (
                  <option key={s.id} value={s.id}>
                    {s.name}
                  </option>
                ))}
              </select>
              <p className="text-xs text-muted-foreground">
                Stored as a new event on that endpoint; nothing leaves RelayLab.
              </p>
              {fields['destination.endpoint_id'] ? (
                <p id={ids.sinkErr} className="text-xs font-medium text-destructive">
                  {fields['destination.endpoint_id']}
                </p>
              ) : null}
            </div>
          )
        ) : (
          <div className="space-y-1.5">
            <Label htmlFor={ids.url}>Destination URL</Label>
            <Input
              id={ids.url}
              type="url"
              inputMode="url"
              placeholder="https://example.com/webhooks"
              value={url}
              onChange={(e) => setUrl(e.target.value)}
              aria-invalid={fields['destination.url'] ? true : undefined}
              aria-describedby={fields['destination.url'] ? ids.urlErr : undefined}
              data-autofocus
            />
            {fields['destination.url'] ? (
              <p id={ids.urlErr} className="text-xs font-medium text-destructive">
                {fields['destination.url']}
              </p>
            ) : (
              <p className="text-xs text-muted-foreground">
                HTTPS only. The URL is used as entered; the original path and query are not added.
              </p>
            )}
          </div>
        )}
      </fieldset>

      <div className="space-y-1.5">
        <Label htmlFor={ids.method}>Method</Label>
        <select
          id={ids.method}
          value={method}
          onChange={(e) => setMethod(e.target.value)}
          className="h-9 w-40 rounded-md border bg-background px-3 text-sm"
        >
          {REPLAY_METHODS.map((m) => (
            <option key={m}>{m}</option>
          ))}
        </select>
      </div>

      <fieldset className="space-y-2" aria-describedby={fields.header_overrides ? ids.ovErr : undefined}>
        <legend className="text-sm font-medium">Added headers</legend>
        <p className="text-xs text-muted-foreground">
          Added headers replace headers of the same name. Their values are stored encrypted, erased when the
          replay finishes, and always shown as [REDACTED].
        </p>
        {overrides.map((o, i) => (
          <div key={o.id} className="flex gap-2">
            <Input
              aria-label={`Header name ${i + 1}`}
              placeholder="Name"
              value={o.name}
              onChange={(e) =>
                setOverrides((all) => all.map((x) => (x.id === o.id ? { ...x, name: e.target.value } : x)))
              }
            />
            <Input
              aria-label={`Header value ${i + 1}`}
              placeholder="Value"
              type="password"
              autoComplete="off"
              value={o.value}
              onChange={(e) =>
                setOverrides((all) => all.map((x) => (x.id === o.id ? { ...x, value: e.target.value } : x)))
              }
            />
            <Button
              variant="ghost"
              size="icon"
              aria-label={`Remove header ${i + 1}`}
              onClick={() => setOverrides((all) => all.filter((x) => x.id !== o.id))}
            >
              <Trash2 aria-hidden="true" />
            </Button>
          </div>
        ))}
        {fields.header_overrides ? (
          <p id={ids.ovErr} className="text-xs font-medium text-destructive">
            {fields.header_overrides}
          </p>
        ) : null}
        <Button
          variant="outline"
          size="sm"
          disabled={overrides.length >= MAX_OVERRIDES}
          onClick={() => setOverrides((all) => [...all, { id: nextId.current++, name: '', value: '' }])}
        >
          <Plus aria-hidden="true" /> Add header
        </Button>
      </fieldset>

      <section
        aria-labelledby="replay-preview-heading"
        className="space-y-2 rounded-md border bg-muted/30 p-3"
      >
        <h3 id="replay-preview-heading" className="text-sm font-semibold">
          Request preview
        </h3>
        <PreviewBody input={input} current={current} pending={preview.isPending} />
      </section>

      {unavailable ? (
        <Alert tone="error">
          <p>Replay is unavailable right now. Try the test sink, or try again later.</p>
        </Alert>
      ) : isApiError(sendError) && !Object.keys(fields).length ? (
        <Alert tone="error">
          <p>{sendError.message}</p>
        </Alert>
      ) : sendError && !isApiError(sendError) ? (
        <Alert tone="error">
          <p>The replay could not be queued. Try again.</p>
        </Alert>
      ) : null}

      <div className="flex justify-end gap-2">
        <Button variant="outline" onClick={onClose}>
          Cancel
        </Button>
        <Button type="submit" disabled={!canSend}>
          {create.isPending ? <Loader2 aria-hidden="true" className="animate-spin" /> : null}
          Send replay
        </Button>
      </div>
    </form>
  )
}

function PreviewBody({
  input,
  current,
  pending,
}: {
  input: ReplayInput | null
  current: { preview?: Preview; error?: unknown } | null
  pending: boolean
}) {
  if (!input) {
    return <p className="text-sm text-muted-foreground">Choose a destination to see the request.</p>
  }
  if (!current || (pending && !current.preview)) {
    return (
      <output className="flex items-center gap-2 text-sm text-muted-foreground">
        <Loader2 aria-hidden="true" className="size-4 animate-spin" /> Preparing preview…
      </output>
    )
  }
  if (!current.preview) {
    return <p className="text-sm text-muted-foreground">Fix the highlighted fields to see the request.</p>
  }
  const p = current.preview
  return (
    <div className="space-y-2 text-sm">
      <p className="font-mono break-all">
        <span className="font-semibold">{p.method}</span> <VisibleText text={p.target} />
      </p>
      <p className="text-muted-foreground">
        Body: {formatBytes(p.body_size)}
        {p.content_type ? (
          <>
            {' '}
            (<VisibleText text={p.content_type} />)
          </>
        ) : null}
      </p>
      <table className="w-full text-xs">
        <caption className="sr-only">Headers that will be sent</caption>
        <thead className="text-left text-muted-foreground">
          <tr>
            <th scope="col" className="py-1 pr-3 font-medium">
              Header sent
            </th>
            <th scope="col" className="py-1 font-medium">
              Value
            </th>
          </tr>
        </thead>
        <tbody>
          {p.headers.map((h, i) => (
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
      {p.stripped_headers.length > 0 ? (
        <p className="flex gap-1.5 text-xs text-muted-foreground">
          <Info aria-hidden="true" className="mt-0.5 size-3.5 shrink-0" />
          <span>
            Not forwarded:{' '}
            <span className="font-mono">
              <VisibleText text={p.stripped_headers.join(', ')} />
            </span>
          </span>
        </p>
      ) : null}
    </div>
  )
}
