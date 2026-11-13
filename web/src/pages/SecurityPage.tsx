import { usePublicConfig } from '@/hooks/session'
import { formatRetention } from '@/lib/format'

/**
 * Security and privacy summary for visitors and users (add-landing-page).
 * The full details live in docs/security/ in the repository.
 */
export function SecurityPage() {
  const config = usePublicConfig()
  const ttl = config.data?.retention?.event_ttl_seconds
  const keep = ttl ? `${formatRetention(ttl)}` : 'the configured retention period'
  return (
    <article className="max-w-2xl space-y-8">
      <header className="space-y-3">
        <h1 className="text-3xl font-semibold tracking-tight">Security and privacy</h1>
        <p className="text-lg leading-relaxed text-muted-foreground">
          Webhooks often carry customer details and tokens. This is what RelayLab stores, for how long, and
          what it will and won’t send on your behalf.
        </p>
      </header>

      <Section title="What is stored">
        <p>
          For each request your endpoint receives: the method, path, query, headers, the body exactly as sent,
          the content type, the time, the user agent, and the sender’s network shortened to a /24 (IPv4) or
          /48 (IPv6) range. Nothing else about the sender is kept.
        </p>
      </Section>

      <Section title="Credentials are removed on arrival">
        <p>
          Values of headers such as Authorization, Cookie, and API keys, and of query parameters such as token
          or signature, are replaced before anything is stored. The page shows the header name and that its
          value was removed; the original value is never written to the database or to logs.
        </p>
      </Section>

      <Section title="How long data is kept">
        <p>
          Requests and their replays are deleted after {keep}. You can delete a single request, or all
          requests of an endpoint, at any time; deleting an endpoint or a project deletes everything in it.
          Deletion is permanent. Demo workspaces, and everything in them, are deleted after 24 hours.
        </p>
      </Section>

      <Section title="What replay can reach">
        <p>
          A replay to a URL goes only to a public internet address over HTTPS. Private networks, localhost,
          cloud metadata services, and RelayLab’s own services are refused, including through DNS tricks and
          alternate address spellings, and redirects are not followed. Signature and credential headers from
          the original request are not forwarded unless you add them yourself; values you add are stored
          encrypted and erased once the replay finishes. A replay to the test sink never leaves RelayLab.
        </p>
      </Section>

      <Section title="What RelayLab does not do">
        <p>
          It does not use your payloads for analytics or to train models, does not render received HTML or
          scripts, and does not share data between accounts: every request is checked against the project’s
          owner.
        </p>
      </Section>
    </article>
  )
}

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <section className="space-y-2">
      <h2 className="text-xl font-semibold">{title}</h2>
      <div className="leading-relaxed text-muted-foreground">{children}</div>
    </section>
  )
}
