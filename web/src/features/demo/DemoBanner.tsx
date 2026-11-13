import { FlaskConical } from 'lucide-react'

import { useMe } from '@/hooks/session'
import { formatDateTime } from '@/lib/format'

/** Shown on every dashboard screen of a demo workspace. */
export function DemoBanner() {
  const me = useMe()
  if (!me.data?.is_demo) return null
  const expires = me.data.demo_expires_at
  return (
    <section aria-label="Demo workspace" className="border-b bg-primary/10">
      <p className="mx-auto flex max-w-6xl flex-wrap items-center gap-x-2 gap-y-1 px-4 py-2 text-sm">
        <FlaskConical aria-hidden="true" className="size-4 text-primary" />
        <strong className="font-semibold">Demo workspace.</strong>
        <span>
          The data here is synthetic.{' '}
          {expires ? (
            <>
              It is deleted on <time dateTime={expires}>{formatDateTime(expires)}</time>.
            </>
          ) : (
            'It is deleted after 24 hours.'
          )}{' '}
          Replay goes to the test sink only, and the demo has one project and two endpoints.
        </span>
      </p>
    </section>
  )
}
