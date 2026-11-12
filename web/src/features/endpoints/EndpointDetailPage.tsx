import { Loader2, Pencil, RefreshCw, Trash2, Unlink } from 'lucide-react'
import { useId, useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router'

import { CopyButton } from '@/components/copy-button'
import { Alert } from '@/components/ui/alert'
import { Button, buttonVariants } from '@/components/ui/button'
import { Dialog } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  curlExample,
  useDeleteEndpoint,
  useEndpoint,
  useRotateEndpoint,
  useUpdateEndpoint,
  type Endpoint,
} from '@/features/endpoints/api'
import { EnabledBadge } from '@/features/endpoints/EnabledBadge'
import { EndpointNameDialog } from '@/features/endpoints/EndpointNameDialog'
import { useCurrentProject } from '@/features/projects/ProjectLayout'
import { isApiError } from '@/lib/api'
import { formatDateTime } from '@/lib/format'
import { cn } from '@/lib/utils'

export function EndpointDetailPage() {
  const project = useCurrentProject()
  const { endpointId = '' } = useParams()
  const endpoint = useEndpoint(project.id, endpointId)

  if (endpoint.isPending) {
    return (
      <output className="flex items-center gap-2 text-muted-foreground">
        <Loader2 aria-hidden="true" className="size-4 animate-spin" /> Loading endpoint…
      </output>
    )
  }
  if (endpoint.isError) {
    if (isApiError(endpoint.error) && endpoint.error.status === 404)
      return <EndpointNotFound projectId={project.id} />
    if (isApiError(endpoint.error) && endpoint.error.status === 401) return null // global handler redirects
    return (
      <Alert tone="error">
        <p>This endpoint could not be loaded.</p>
        <Button variant="outline" size="sm" onClick={() => void endpoint.refetch()}>
          Try again
        </Button>
      </Alert>
    )
  }
  return <EndpointDetail projectId={project.id} endpoint={endpoint.data} />
}

function EndpointDetail({ projectId, endpoint: e }: { projectId: string; endpoint: Endpoint }) {
  const update = useUpdateEndpoint(projectId, e.id)
  const [renaming, setRenaming] = useState(false)

  return (
    <div className="max-w-3xl space-y-6">
      <div className="space-y-2">
        <p className="text-sm text-muted-foreground">
          <Link to={`/projects/${projectId}/endpoints`} className="underline-offset-4 hover:underline">
            Endpoints
          </Link>
        </p>
        <div className="flex flex-wrap items-center gap-3">
          <h2 className="text-xl font-semibold break-words">{e.name}</h2>
          <EnabledBadge enabled={e.enabled} />
          <Button variant="ghost" size="sm" onClick={() => setRenaming(true)}>
            <Pencil aria-hidden="true" /> Rename
          </Button>
        </div>
        <p className="text-xs text-muted-foreground">
          Created {formatDateTime(e.created_at)} · Last received{' '}
          {e.last_received_at ? formatDateTime(e.last_received_at) : 'never'}
        </p>
      </div>

      <ReceivingUrl endpoint={e} />
      <EnabledSection projectId={projectId} endpoint={e} />
      <RotateSection projectId={projectId} endpoint={e} />
      <DeleteSection projectId={projectId} endpoint={e} />

      <EndpointNameDialog
        open={renaming}
        onClose={() => setRenaming(false)}
        title="Rename endpoint"
        description="The receiving URL does not change."
        submitLabel="Save name"
        pendingLabel="Saving…"
        initialName={e.name}
        onSubmit={async (name) => {
          await update.mutateAsync({ name })
          setRenaming(false)
        }}
      />
    </div>
  )
}

function ReceivingUrl({ endpoint: e }: { endpoint: Endpoint }) {
  if (!e.url) {
    return (
      <Alert tone="error">
        <p>
          This endpoint&apos;s receiving URL cannot be shown. Rotate the URL below to issue a new one; the old
          URL stops working.
        </p>
      </Alert>
    )
  }
  const curl = curlExample(e.url)
  return (
    <section aria-labelledby="url-heading" className="space-y-4 rounded-lg border bg-card p-6">
      <div className="space-y-1">
        <h3 id="url-heading" className="font-semibold">
          Receiving URL
        </h3>
        <p className="text-sm text-muted-foreground">
          Anyone with this URL can send requests to this endpoint. Treat it like a password.
        </p>
      </div>
      <div className="flex flex-wrap items-center gap-2">
        <code className="min-w-0 flex-1 rounded-md border bg-muted px-3 py-2 font-mono text-sm break-all">
          {e.url}
        </code>
        <CopyButton value={e.url} label="Copy URL" />
      </div>
      {!e.enabled ? (
        <p className="text-sm text-muted-foreground">
          This endpoint is disabled, so requests to this URL are rejected until you enable it.
        </p>
      ) : null}
      <Alert tone="info">
        <p>
          RelayLab stores each request and replies with{' '}
          <code className="font-mono">{'{"ok":true,"event_id":"…"}'}</code>. Values of credential headers and
          query parameters, such as Authorization and Cookie, are replaced with{' '}
          <code className="font-mono">[REDACTED]</code> before storage.{' '}
          <Link
            to={`/projects/${e.project_id}/events?endpoint_id=${e.id}`}
            className="font-medium underline underline-offset-4"
          >
            View this endpoint&apos;s events
          </Link>
        </p>
      </Alert>
      <div className="space-y-2">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <h4 className="text-sm font-medium">Send a test request</h4>
          <CopyButton value={curl} label="Copy curl command" />
        </div>
        <pre className="overflow-x-auto rounded-md border bg-muted px-3 py-2 font-mono text-xs whitespace-pre-wrap break-all">
          {curl}
        </pre>
      </div>
    </section>
  )
}

function EnabledSection({ projectId, endpoint: e }: { projectId: string; endpoint: Endpoint }) {
  const update = useUpdateEndpoint(projectId, e.id)
  const labelId = useId()
  const [error, setError] = useState<string | null>(null)

  async function toggle() {
    setError(null)
    try {
      await update.mutateAsync({ enabled: !e.enabled })
    } catch {
      setError(`The endpoint could not be ${e.enabled ? 'disabled' : 'enabled'}. Try again.`)
    }
  }

  return (
    <section aria-labelledby={labelId} className="space-y-3 rounded-lg border bg-card p-6">
      <div className="flex items-start justify-between gap-4">
        <div className="space-y-1">
          <h3 id={labelId} className="font-semibold">
            Accept requests
          </h3>
          <p className="text-sm text-muted-foreground">
            Disabling keeps the URL and the events already received, but rejects new requests.
          </p>
        </div>
        <button
          type="button"
          role="switch"
          aria-checked={e.enabled}
          aria-labelledby={labelId}
          disabled={update.isPending}
          onClick={() => void toggle()}
          className={cn(
            'relative inline-flex h-6 w-11 shrink-0 items-center rounded-full border-2 border-transparent transition-colors focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:outline-none disabled:opacity-50',
            e.enabled ? 'bg-primary' : 'bg-input',
          )}
        >
          <span
            aria-hidden="true"
            className={cn(
              'size-5 rounded-full bg-background shadow transition-transform',
              e.enabled ? 'translate-x-5' : 'translate-x-0',
            )}
          />
        </button>
      </div>
      {error ? <Alert tone="error">{error}</Alert> : null}
    </section>
  )
}

function RotateSection({ projectId, endpoint: e }: { projectId: string; endpoint: Endpoint }) {
  const rotate = useRotateEndpoint(projectId, e.id)
  const [open, setOpen] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [rotated, setRotated] = useState(false)

  function close() {
    setError(null)
    setOpen(false)
  }

  async function confirm() {
    setError(null)
    try {
      await rotate.mutateAsync()
      setOpen(false)
      setRotated(true)
    } catch {
      setError('The URL could not be rotated. The current URL still works. Try again.')
    }
  }

  return (
    <section aria-labelledby="rotate-heading" className="space-y-3 rounded-lg border bg-card p-6">
      <div className="space-y-1">
        <h3 id="rotate-heading" className="font-semibold">
          Rotate URL
        </h3>
        <p className="text-sm text-muted-foreground">
          Issue a new receiving URL if the current one has leaked. Events already received are kept.
        </p>
      </div>
      {rotated ? <Alert tone="success">New URL issued. Update your webhook provider with it.</Alert> : null}
      <Button
        variant="outline"
        onClick={() => {
          setRotated(false)
          setOpen(true)
        }}
      >
        <RefreshCw aria-hidden="true" /> Rotate URL
      </Button>
      <Dialog
        open={open}
        onClose={close}
        title="Rotate receiving URL?"
        description="The current URL stops working immediately. Requests sent to it are rejected, so update your webhook provider with the new URL."
      >
        <div className="space-y-4">
          {error ? <Alert tone="error">{error}</Alert> : null}
          <div className="flex justify-end gap-2">
            <Button variant="ghost" onClick={close} data-autofocus>
              Cancel
            </Button>
            <Button variant="destructive" onClick={() => void confirm()} disabled={rotate.isPending}>
              {rotate.isPending ? <Loader2 aria-hidden="true" className="animate-spin" /> : null}
              {rotate.isPending ? 'Rotating…' : 'Rotate URL'}
            </Button>
          </div>
        </div>
      </Dialog>
    </section>
  )
}

function DeleteSection({ projectId, endpoint: e }: { projectId: string; endpoint: Endpoint }) {
  const del = useDeleteEndpoint(projectId, e.id)
  const navigate = useNavigate()
  const inputId = useId()
  const [open, setOpen] = useState(false)
  const [typed, setTyped] = useState('')
  const [error, setError] = useState<string | null>(null)
  const matches = typed === e.name

  function close() {
    setTyped('')
    setError(null)
    setOpen(false)
  }

  async function onDelete(ev: React.FormEvent) {
    ev.preventDefault()
    if (!matches) return
    setError(null)
    try {
      await del.mutateAsync()
      void navigate(`/projects/${projectId}/endpoints`, { replace: true })
    } catch {
      setError('The endpoint could not be deleted. Try again.')
    }
  }

  return (
    <section
      aria-labelledby="delete-heading"
      className="space-y-3 rounded-lg border border-destructive/40 bg-card p-6"
    >
      <div className="space-y-1">
        <h3 id="delete-heading" className="font-semibold">
          Delete endpoint
        </h3>
        <p className="text-sm text-muted-foreground">
          Permanently deletes this endpoint and all its events. Its URL stops accepting requests immediately.
        </p>
      </div>
      <Button variant="destructive" onClick={() => setOpen(true)}>
        <Trash2 aria-hidden="true" /> Delete endpoint
      </Button>
      <Dialog
        open={open}
        onClose={close}
        title="Delete endpoint?"
        description="This deletes the endpoint and all its events. It cannot be undone."
      >
        <form onSubmit={(ev) => void onDelete(ev)} className="space-y-4" aria-label="Delete endpoint">
          {error ? <Alert tone="error">{error}</Alert> : null}
          <div className="space-y-1.5">
            <Label htmlFor={inputId}>
              Type <span className="font-mono break-all">{e.name}</span> to confirm
            </Label>
            <Input
              id={inputId}
              autoComplete="off"
              data-autofocus
              value={typed}
              onChange={(ev) => setTyped(ev.target.value)}
            />
          </div>
          <div className="flex justify-end gap-2">
            <Button variant="ghost" onClick={close}>
              Cancel
            </Button>
            <Button type="submit" variant="destructive" disabled={!matches || del.isPending}>
              {del.isPending ? <Loader2 aria-hidden="true" className="animate-spin" /> : null}
              {del.isPending ? 'Deleting…' : 'Delete endpoint'}
            </Button>
          </div>
        </form>
      </Dialog>
    </section>
  )
}

function EndpointNotFound({ projectId }: { projectId: string }) {
  return (
    <section className="mx-auto flex max-w-md flex-col items-center gap-3 py-16 text-center">
      <Unlink aria-hidden="true" className="size-8 text-muted-foreground" />
      <h2 className="text-xl font-semibold">Endpoint not found</h2>
      <p className="text-sm text-muted-foreground">It may have been deleted, or the link is wrong.</p>
      <Link to={`/projects/${projectId}/endpoints`} className={buttonVariants({ variant: 'outline' })}>
        Back to endpoints
      </Link>
    </section>
  )
}
