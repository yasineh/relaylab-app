import { useQuery } from '@tanstack/react-query'
import { CircleAlert, CircleCheck, Loader2 } from 'lucide-react'

import { fetchReadiness } from '@/lib/api'

export function HomePage() {
  return (
    <div className="space-y-8">
      <section className="space-y-3">
        <h1 className="text-3xl font-semibold tracking-tight">RelayLab</h1>
        <p className="max-w-2xl text-muted-foreground">
          A developer workspace to receive, inspect, replay, and debug webhooks. The platform foundation is
          running; projects, endpoints, and the event explorer arrive in upcoming releases.
        </p>
      </section>
      <SystemStatus />
    </div>
  )
}

function SystemStatus() {
  const { data, isPending, isError, refetch, isFetching } = useQuery({
    queryKey: ['readiness'],
    queryFn: ({ signal }) => fetchReadiness(signal),
  })

  let body
  if (isPending) {
    body = (
      <p className="flex items-center gap-2 text-muted-foreground">
        <Loader2 aria-hidden="true" className="size-4 animate-spin" /> Checking API status…
      </p>
    )
  } else if (isError) {
    body = (
      <p className="flex items-center gap-2 text-destructive">
        <CircleAlert aria-hidden="true" className="size-4" /> The API is unreachable.
      </p>
    )
  } else if (data.status === 'ready') {
    body = (
      <p className="flex items-center gap-2 text-success">
        <CircleCheck aria-hidden="true" className="size-4" /> API and database are ready.
      </p>
    )
  } else {
    body = (
      <div className="space-y-1">
        <p className="flex items-center gap-2 text-destructive">
          <CircleAlert aria-hidden="true" className="size-4" /> The API is not ready.
        </p>
        <p className="text-sm text-muted-foreground">
          Database: {data.checks.database} · Migrations: {data.checks.migrations}
        </p>
      </div>
    )
  }

  return (
    <section aria-labelledby="status-heading" className="max-w-md rounded-lg border bg-card p-5">
      <div className="mb-3 flex items-center justify-between">
        <h2 id="status-heading" className="font-medium">
          System status
        </h2>
        <button
          type="button"
          onClick={() => void refetch()}
          disabled={isFetching}
          className="text-sm text-primary underline-offset-4 hover:underline disabled:opacity-50"
        >
          Refresh
        </button>
      </div>
      <div aria-live="polite">{body}</div>
    </section>
  )
}
