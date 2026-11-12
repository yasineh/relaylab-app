import { Webhook } from 'lucide-react'
import { Link } from 'react-router'

import { buttonVariants } from '@/components/ui/button'

import { RecentEvents } from '@/features/events/RecentEvents'
import { useCurrentProject } from '@/features/projects/ProjectLayout'
import { formatDateTime } from '@/lib/format'

export function ProjectOverviewPage() {
  const project = useCurrentProject()
  return (
    <div className="space-y-6">
      {project.description ? (
        <p className="max-w-2xl whitespace-pre-line break-words text-muted-foreground">
          {project.description}
        </p>
      ) : null}

      <section aria-labelledby="setup-heading" className="max-w-2xl rounded-lg border bg-card p-6">
        <h2 id="setup-heading" className="font-semibold">
          Set up this project
        </h2>
        <ol className="mt-3 list-decimal space-y-1 pl-5 text-sm text-muted-foreground">
          <li>Create a webhook endpoint to get a unique receiving URL.</li>
          <li>Point a webhook provider at that URL, or send a test request with curl.</li>
          <li>Inspect each request as it arrives, then replay it to a destination you control.</li>
        </ol>
      </section>

      <div className="grid gap-4 md:grid-cols-2">
        <section aria-labelledby="endpoints-heading" className="rounded-lg border bg-card p-6">
          <Webhook aria-hidden="true" className="mb-2 size-5 text-primary" />
          <h2 id="endpoints-heading" className="font-medium">
            Endpoints
          </h2>
          <p className="mt-1 text-sm text-muted-foreground">
            Create an endpoint to get a unique URL for receiving webhooks.
          </p>
          <Link
            to={`/projects/${project.id}/endpoints`}
            className={buttonVariants({ variant: 'outline', size: 'sm', className: 'mt-3' })}
          >
            Manage endpoints
          </Link>
        </section>
        <RecentEvents projectId={project.id} />
      </div>

      <p className="text-xs text-muted-foreground">
        Created {formatDateTime(project.created_at)} · Updated {formatDateTime(project.updated_at)}
      </p>
    </div>
  )
}
