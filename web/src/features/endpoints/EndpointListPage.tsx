import { Loader2, Plus, Webhook } from 'lucide-react'
import { useState } from 'react'
import { Link, useNavigate } from 'react-router'

import { Alert } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { useCreateEndpoint, useEndpoints, type Endpoint } from '@/features/endpoints/api'
import { EndpointNameDialog } from '@/features/endpoints/EndpointNameDialog'
import { EnabledBadge } from '@/features/endpoints/EnabledBadge'
import { useCurrentProject } from '@/features/projects/ProjectLayout'
import { formatDate, formatDateTime } from '@/lib/format'

export function EndpointListPage() {
  const project = useCurrentProject()
  const endpoints = useEndpoints(project.id)
  const create = useCreateEndpoint(project.id)
  const navigate = useNavigate()
  const [creating, setCreating] = useState(false)

  return (
    <div className="space-y-6">
      <div className="flex flex-wrap items-center justify-between gap-4">
        <p className="max-w-2xl text-muted-foreground">
          Each endpoint has its own secret receiving URL. Requests sent to it are stored in this project.
        </p>
        {endpoints.data && endpoints.data.length > 0 ? (
          <Button onClick={() => setCreating(true)}>
            <Plus aria-hidden="true" /> Create endpoint
          </Button>
        ) : null}
      </div>

      {endpoints.isPending ? (
        <output className="flex items-center gap-2 text-muted-foreground">
          <Loader2 aria-hidden="true" className="size-4 animate-spin" /> Loading endpoints…
        </output>
      ) : endpoints.isError ? (
        <Alert tone="error">
          <p>The endpoints could not be loaded.</p>
          <Button variant="outline" size="sm" onClick={() => void endpoints.refetch()}>
            Try again
          </Button>
        </Alert>
      ) : endpoints.data.length === 0 ? (
        <EmptyState onCreate={() => setCreating(true)} />
      ) : (
        <EndpointTable projectId={project.id} endpoints={endpoints.data} />
      )}

      <EndpointNameDialog
        open={creating}
        onClose={() => setCreating(false)}
        title="Create endpoint"
        description="You get a unique receiving URL. You can rename the endpoint later."
        submitLabel="Create endpoint"
        pendingLabel="Creating…"
        onSubmit={async (name) => {
          const e = await create.mutateAsync(name)
          setCreating(false)
          void navigate(`/projects/${project.id}/endpoints/${e.id}`)
        }}
      />
    </div>
  )
}

function EmptyState({ onCreate }: { onCreate: () => void }) {
  return (
    <section className="flex max-w-xl flex-col items-start gap-3 rounded-lg border border-dashed bg-card p-6">
      <Webhook aria-hidden="true" className="size-6 text-primary" />
      <h2 className="font-medium">Create your first endpoint</h2>
      <p className="text-sm text-muted-foreground">
        An endpoint gives you a unique URL to point a webhook provider at, or to send a test request to with
        curl.
      </p>
      <Button onClick={onCreate}>
        <Plus aria-hidden="true" /> Create endpoint
      </Button>
    </section>
  )
}

function EndpointTable({ projectId, endpoints }: { projectId: string; endpoints: Endpoint[] }) {
  return (
    <div className="overflow-x-auto rounded-lg border bg-card">
      <table className="w-full text-sm">
        <caption className="sr-only">Endpoints in this project, newest first</caption>
        <thead className="border-b text-left text-xs text-muted-foreground">
          <tr>
            <th scope="col" className="px-4 py-2.5 font-medium">
              Name
            </th>
            <th scope="col" className="px-4 py-2.5 font-medium">
              Status
            </th>
            <th scope="col" className="px-4 py-2.5 font-medium">
              Last received
            </th>
            <th scope="col" className="px-4 py-2.5 font-medium">
              Created
            </th>
          </tr>
        </thead>
        <tbody className="divide-y">
          {endpoints.map((e) => (
            <tr key={e.id}>
              <th scope="row" className="max-w-xs px-4 py-3 text-left font-medium">
                <Link
                  to={`/projects/${projectId}/endpoints/${e.id}`}
                  className="break-words underline-offset-4 hover:underline"
                >
                  {e.name}
                </Link>
              </th>
              <td className="px-4 py-3">
                <EnabledBadge enabled={e.enabled} />
              </td>
              <td className="px-4 py-3 whitespace-nowrap text-muted-foreground">
                {e.last_received_at ? formatDateTime(e.last_received_at) : 'Never'}
              </td>
              <td className="px-4 py-3 whitespace-nowrap text-muted-foreground">
                {formatDate(e.created_at)}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}
