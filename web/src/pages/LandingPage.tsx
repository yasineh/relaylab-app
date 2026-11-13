import { LayoutDashboard, PlayCircle } from 'lucide-react'
import { Link } from 'react-router'

import inspectShot from '@/assets/screenshots/inspect.jpg'
import receiveShot from '@/assets/screenshots/receive.jpg'
import replayShot from '@/assets/screenshots/replay.jpg'
import { buttonVariants } from '@/components/ui/button'
import { TryDemoButton } from '@/features/demo/TryDemo'
import { useMe, usePublicConfig } from '@/hooks/session'
import { formatRetention } from '@/lib/format'

/**
 * Public landing page (add-landing-page). Every claim here describes a
 * shipped capability; the screenshots are real RelayLab screens showing a
 * demo workspace's synthetic data (captured by e2e/tools/screenshots.ts).
 */
export function LandingPage() {
  return (
    <div className="space-y-24 pb-8">
      <Hero />
      <HowItWorks />
      <Features />
      <Safety />
    </div>
  )
}

function Hero() {
  return (
    <section
      aria-labelledby="landing-title"
      className="grid items-start gap-10 lg:grid-cols-[minmax(0,1fr)_minmax(0,1.1fr)]"
    >
      <div className="max-w-xl space-y-6 lg:pt-6">
        <h1
          id="landing-title"
          className="text-4xl leading-tight font-semibold tracking-tight text-balance sm:text-5xl"
        >
          Catch a webhook, read every byte, send it again.
        </h1>
        <p className="text-lg leading-relaxed text-muted-foreground">
          RelayLab gives you a URL that records every request sent to it. Inspect the exact method, headers,
          query, and body, then replay the request to a server you control or to a built-in test sink.
        </p>
        <CallsToAction />
      </div>
      <RequestPanel />
    </section>
  )
}

function CallsToAction() {
  const config = usePublicConfig()
  const me = useMe()
  const video = config.data?.demo_video_url
  const signedIn = Boolean(me.data)
  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-start gap-3">
        {signedIn ? (
          <Link to="/projects" className={buttonVariants({ size: 'lg' })}>
            <LayoutDashboard aria-hidden="true" /> Open dashboard
          </Link>
        ) : (
          <>
            {config.data?.demo_enabled ? <TryDemoButton size="lg" /> : null}
            {config.data?.signup_enabled ? (
              <Link
                to="/sign-up"
                className={buttonVariants({
                  size: 'lg',
                  variant: config.data.demo_enabled ? 'outline' : 'default',
                })}
              >
                Create account
              </Link>
            ) : null}
            <Link to="/sign-in" className={buttonVariants({ size: 'lg', variant: 'ghost' })}>
              Sign in
            </Link>
          </>
        )}
      </div>
      {video ? (
        <a
          href={video}
          target="_blank"
          rel="noopener noreferrer"
          className="inline-flex items-center gap-2 text-sm font-medium text-primary underline-offset-4 hover:underline"
        >
          <PlayCircle aria-hidden="true" className="size-4" /> Watch the demo (opens in a new tab)
        </a>
      ) : null}
    </div>
  )
}

/**
 * A received request as RelayLab stores it: the /orders event from the demo
 * workspace's seed data, with its credentials removed at receipt.
 */
function RequestPanel() {
  return (
    <figure className="overflow-hidden rounded-xl border border-slate-700 bg-[#1E293B] text-slate-100 shadow-lg">
      <figcaption className="flex flex-wrap items-center justify-between gap-2 border-b border-slate-700 px-5 py-3 text-sm">
        <span className="text-slate-300">Received by Demo endpoint</span>
        <span className="text-slate-400">71 bytes</span>
      </figcaption>
      <pre className="overflow-x-auto px-5 py-4 font-mono text-[13px] leading-6 whitespace-pre-wrap break-all">
        <code>
          <span className="font-semibold text-sky-300">POST</span> /orders?store=demo&amp;token=
          <span className="rounded bg-slate-700 px-1 text-amber-200">[REDACTED]</span>
          {'\n'}
          <span className="text-slate-400">Authorization:</span>{' '}
          <span className="rounded bg-slate-700 px-1 text-amber-200">removed at receipt</span>
          {'\n'}
          <span className="text-slate-400">Content-Type:</span> application/x-www-form-urlencoded
          {'\n'}
          <span className="text-slate-400">User-Agent:</span> ExampleShop/1.0
          {'\n\n'}
          order_id=demo-1001&amp;total=19.90&amp;currency=EUR&amp;email=shopper%40example.net
        </code>
      </pre>
      <p className="flex flex-wrap items-center gap-2 border-t border-slate-700 px-5 py-3 text-sm text-slate-300">
        <span className="inline-flex items-center gap-1.5 rounded-full bg-emerald-500/15 px-2 py-0.5 font-medium text-emerald-300">
          <span aria-hidden="true" className="size-1.5 rounded-full bg-emerald-400" />
          200
        </span>
        Replayed to the test sink in 2 ms
      </p>
    </figure>
  )
}

const STEPS = [
  {
    title: 'Receive',
    text: 'Create an endpoint and point your provider, or curl, at its URL. Each request is stored before RelayLab answers, and appears in your event list as it arrives, without a reload.',
    image: receiveShot,
    alt: 'The event list of a demo project: eight received requests with their method, path, content type, size, and time, and a Live indicator.',
  },
  {
    title: 'Inspect',
    text: 'Open a request to see its body as a JSON tree, plain text, or hex, with every header and query parameter. Credentials such as Authorization are removed when the request arrives.',
    image: inspectShot,
    alt: 'The headers of a received request: Authorization shows "Removed at receipt", followed by Content-Type and User-Agent.',
  },
  {
    title: 'Replay',
    text: 'Send the stored request again to an HTTPS URL you control, or to a test sink inside RelayLab. The response status, headers, and body are kept with the replay.',
    image: replayShot,
    alt: 'Replay details: succeeded with status 200 to the test sink in 2 ms, with the response headers and body.',
  },
] as const

function HowItWorks() {
  return (
    <section aria-labelledby="how-title" className="space-y-10">
      <div className="max-w-2xl space-y-3">
        <h2 id="how-title" className="text-2xl font-semibold tracking-tight sm:text-3xl">
          How it works
        </h2>
        <p className="text-muted-foreground">
          Webhook bugs are hard to reproduce. The provider sends a request once, to a URL that may not be
          yours, and what it sent is gone unless you logged it. RelayLab keeps the request so you can look at
          it and send it again.
        </p>
      </div>
      <ol className="space-y-14">
        {STEPS.map((s, i) => (
          <li
            key={s.title}
            className="grid items-center gap-6 md:grid-cols-[minmax(0,2fr)_minmax(0,3fr)] md:gap-10"
          >
            <div className="space-y-2">
              <p aria-hidden="true" className="font-mono text-sm text-primary">
                Step {i + 1}
              </p>
              <h3 className="text-xl font-semibold">{s.title}</h3>
              <p className="leading-relaxed text-muted-foreground">{s.text}</p>
            </div>
            <img
              src={s.image}
              alt={s.alt}
              loading="lazy"
              className="w-full rounded-lg border bg-card shadow-sm"
            />
          </li>
        ))}
      </ol>
    </section>
  )
}

function Features() {
  const config = usePublicConfig()
  const ttl = config.data?.retention?.event_ttl_seconds
  const features: [string, string][] = [
    [
      'A URL per endpoint',
      'Each endpoint gets an unguessable receiving URL. Rotate it if it leaks, or disable it to stop accepting requests.',
    ],
    [
      'The request as sent',
      'Method, path, query, headers, and the body byte for byte, including binary bodies and JSON that does not parse.',
    ],
    [
      'Search the history',
      'Filter by endpoint, method, time, or content type, and copy any request as a curl command.',
    ],
    [
      'Live, and it catches up',
      'New requests appear while the page is open. After a dropped connection, the ones you missed load on reconnect.',
    ],
    [
      'Replay with guard rails',
      'URL replays go to public HTTPS addresses only, never to private networks or cloud metadata, and redirects are not followed.',
    ],
    [
      'Data that expires',
      ttl
        ? `Requests are deleted after ${formatRetention(ttl)}, and you can delete them sooner.`
        : 'Delete a request, or all of an endpoint’s requests, whenever you want.',
    ],
  ]
  return (
    <section aria-labelledby="features-title" className="space-y-8">
      <div className="max-w-2xl space-y-3">
        <h2 id="features-title" className="text-2xl font-semibold tracking-tight sm:text-3xl">
          What you get
        </h2>
        <p className="text-muted-foreground">
          Built for developers integrating payment providers, Git hosts, and other services that send
          webhooks.
        </p>
      </div>
      <dl className="grid gap-x-12 gap-y-8 border-t pt-8 sm:grid-cols-2">
        {features.map(([title, text]) => (
          <div key={title} className="space-y-1.5">
            <dt className="font-semibold">{title}</dt>
            <dd className="leading-relaxed text-muted-foreground">{text}</dd>
          </div>
        ))}
      </dl>
    </section>
  )
}

function Safety() {
  return (
    <section aria-labelledby="safety-title" className="max-w-2xl space-y-3 border-l-2 border-primary pl-6">
      <h2 id="safety-title" className="text-xl font-semibold">
        Your payloads stay yours
      </h2>
      <p className="leading-relaxed text-muted-foreground">
        Credential headers and query values are discarded on arrival, payloads are never used for analytics,
        and replays can only reach public addresses.{' '}
        <Link to="/security" className="font-medium text-foreground underline underline-offset-4">
          Read how RelayLab handles your data
        </Link>
        .
      </p>
    </section>
  )
}
