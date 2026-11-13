import { Alert } from '@/components/ui/alert'
import { usePublicConfig } from '@/hooks/session'
import { formatRetention } from '@/lib/format'

/** The retention sentence for the current deployment, or null until the config has loaded. */
export function useRetentionText(): string | null {
  const config = usePublicConfig()
  const r = config.data?.retention
  if (!r) return null
  if (r.event_ttl_seconds <= 0) return 'Events are not deleted automatically in this environment.'
  return `Events are deleted after ${formatRetention(r.event_ttl_seconds)}.`
}

/** "Events are deleted after 7 days." as a muted line. */
export function RetentionNote({ className }: { className?: string }) {
  const text = useRetentionText()
  if (!text) return null
  return <p className={className ?? 'text-sm text-muted-foreground'}>{text}</p>
}

/** The privacy notice for pages where requests are received or listed. */
export function PrivacyNotice() {
  const config = usePublicConfig()
  const r = config.data?.retention
  const kept =
    r && r.event_ttl_seconds > 0
      ? `They are stored for ${formatRetention(r.event_ttl_seconds)}, then deleted automatically, unless you delete them sooner.`
      : 'They are stored until you delete them.'
  return (
    <Alert tone="info">
      <p>
        Received payloads may contain confidential or personal data, such as customer details or tokens your
        provider includes. {kept}
      </p>
    </Alert>
  )
}
