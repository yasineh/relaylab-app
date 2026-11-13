import { useQueryClient } from '@tanstack/react-query'
import { History, Loader2, Send } from 'lucide-react'
import { useState } from 'react'
import { Link } from 'react-router'

import { Button } from '@/components/ui/button'
import { useEndpoints } from '@/features/endpoints/api'
import { VisibleText } from '@/features/events/payload/RawText'
import { stateLabel, useEventReplays, type Replay } from '@/features/replay/api'
import { ReplayDialog } from '@/features/replay/ReplayDialog'
import { ReplayStateBadge } from '@/features/replay/ReplayStateBadge'
import { configQueryKey, useMe, usePublicConfig } from '@/hooks/session'
import { formatPreciseDateTime } from '@/lib/format'

/** The event detail's replay entry point and the event's recent replays. */
export function EventReplayPanel({
  projectId,
  event,
}: {
  projectId: string
  event: { id: string; method: string; endpoint_id: string }
}) {
  const client = useQueryClient()
  const config = usePublicConfig()
  const me = useMe()
  const endpoints = useEndpoints(projectId)
  const replays = useEventReplays(projectId, event.id)
  const [open, setOpen] = useState(false)
  // Test-sink replay is always available; replay_enabled governs URL replay only.
  const urlEnabled = config.data?.replay_enabled === true

  const openDialog = () => {
    // Availability can change while the page is open; re-check it.
    void client.invalidateQueries({ queryKey: configQueryKey })
    setOpen(true)
  }

  return (
    <section aria-labelledby="replays-heading" className="space-y-3 rounded-lg border bg-card p-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h2 id="replays-heading" className="flex items-center gap-2 font-medium">
          <History aria-hidden="true" className="size-4 text-primary" /> Replays
        </h2>
        <Button size="sm" onClick={openDialog} disabled={config.isPending}>
          <Send aria-hidden="true" /> Replay
        </Button>
      </div>

      {replays.isPending ? (
        <output className="flex items-center gap-2 text-sm text-muted-foreground">
          <Loader2 aria-hidden="true" className="size-4 animate-spin" /> Loading replays…
        </output>
      ) : replays.isError ? (
        <p className="text-sm text-destructive">Replays could not be loaded.</p>
      ) : replays.data.items.length === 0 ? (
        <p className="text-sm text-muted-foreground">
          Not replayed yet. A replay re-sends this request to a test sink or a URL you control.
        </p>
      ) : (
        <ul aria-label="Replays of this event" className="divide-y text-sm">
          {replays.data.items.map((r: Replay) => (
            <li key={r.id} className="flex flex-wrap items-center gap-3 py-2">
              <ReplayStateBadge replay={r} />
              <span className="min-w-0 flex-1 truncate font-mono">
                <VisibleText text={`${r.method} ${r.target}`} />
              </span>
              <time dateTime={r.created_at} className="text-xs text-muted-foreground">
                {formatPreciseDateTime(r.created_at)}
              </time>
              <Link
                to={`/projects/${projectId}/replays?replay=${r.id}`}
                className="text-xs underline underline-offset-4"
                aria-label={`Details of replay: ${stateLabel(r)}`}
              >
                Details
              </Link>
            </li>
          ))}
        </ul>
      )}

      <ReplayDialog
        open={open}
        onClose={() => setOpen(false)}
        onQueued={() => setOpen(false)}
        projectId={projectId}
        event={event}
        endpoints={endpoints.data ?? []}
        urlAvailable={config.data?.replay_url_available === true}
        urlEnabled={urlEnabled}
        demo={me.data?.is_demo === true}
      />
    </section>
  )
}
