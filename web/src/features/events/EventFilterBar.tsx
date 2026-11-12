import { useId, useState, type FormEvent } from 'react'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import type { Endpoint } from '@/features/endpoints/api'
import { METHODS, type EventFilters } from '@/features/events/api'

/** ISO (UTC) to the value of a datetime-local input, in the viewer's time zone. */
export function isoToLocalInput(iso: string | undefined): string {
  if (!iso) return ''
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return ''
  const local = new Date(d.getTime() - d.getTimezoneOffset() * 60_000)
  return local.toISOString().slice(0, 16)
}

/** A datetime-local value (viewer's time zone) to ISO in UTC. */
export function localInputToIso(value: string): string | undefined {
  if (!value) return undefined
  const d = new Date(value)
  return Number.isNaN(d.getTime()) ? undefined : d.toISOString()
}

const selectClass =
  'flex h-10 w-full rounded-md border border-input bg-card px-3 py-2 text-sm text-foreground shadow-xs'

/**
 * Filters for the event list. Changes apply when the form is submitted, so
 * each applied filter set is one URL (shareable, back-button friendly).
 */
export function EventFilterBar({
  filters,
  endpoints,
  fieldErrors,
  onApply,
  onClear,
}: {
  filters: EventFilters
  endpoints: Endpoint[]
  fieldErrors?: Record<string, string>
  onApply: (f: EventFilters) => void
  onClear: () => void
}) {
  const id = useId()
  const [endpointId, setEndpointId] = useState(filters.endpoint_id ?? '')
  const [methods, setMethods] = useState<string[]>(filters.method ?? [])
  const [after, setAfter] = useState(isoToLocalInput(filters.received_after))
  const [before, setBefore] = useState(isoToLocalInput(filters.received_before))
  const [contentType, setContentType] = useState(filters.content_type ?? '')
  const [sort, setSort] = useState<'asc' | 'desc'>(filters.sort ?? 'desc')

  function submit(e: FormEvent) {
    e.preventDefault()
    onApply({
      endpoint_id: endpointId || undefined,
      method: methods.length ? methods : undefined,
      received_after: localInputToIso(after),
      received_before: localInputToIso(before),
      content_type: contentType.trim() || undefined,
      sort,
    })
  }

  const err = (field: string) =>
    fieldErrors?.[field] ? (
      <p id={`${id}-${field}-error`} className="text-xs text-destructive">
        {fieldErrors[field]}
      </p>
    ) : null
  const describedBy = (field: string) => (fieldErrors?.[field] ? `${id}-${field}-error` : undefined)

  return (
    <form
      onSubmit={submit}
      aria-label="Filter events"
      className="grid gap-4 rounded-lg border bg-card p-4 sm:grid-cols-2 lg:grid-cols-4"
    >
      <div className="space-y-1.5">
        <Label htmlFor={`${id}-endpoint`}>Endpoint</Label>
        <select
          id={`${id}-endpoint`}
          className={selectClass}
          value={endpointId}
          onChange={(e) => setEndpointId(e.target.value)}
          aria-invalid={Boolean(fieldErrors?.endpoint_id)}
          aria-describedby={describedBy('endpoint_id')}
        >
          <option value="">All endpoints</option>
          {endpoints.map((ep) => (
            <option key={ep.id} value={ep.id}>
              {ep.name}
            </option>
          ))}
        </select>
        {err('endpoint_id')}
      </div>

      <fieldset className="space-y-1.5 sm:col-span-1 lg:col-span-3" aria-describedby={describedBy('method')}>
        <legend className="text-sm font-medium">Methods</legend>
        <div className="flex flex-wrap gap-x-4 gap-y-2 pt-1">
          {METHODS.map((m) => (
            <label key={m} className="inline-flex items-center gap-1.5 font-mono text-sm">
              <input
                type="checkbox"
                className="size-4 accent-primary"
                checked={methods.includes(m)}
                onChange={(e) =>
                  setMethods((cur) => (e.target.checked ? [...cur, m] : cur.filter((x) => x !== m)))
                }
              />
              {m}
            </label>
          ))}
        </div>
        {err('method')}
      </fieldset>

      <div className="space-y-1.5">
        <Label htmlFor={`${id}-after`}>Received after</Label>
        <Input
          id={`${id}-after`}
          type="datetime-local"
          value={after}
          onChange={(e) => setAfter(e.target.value)}
          aria-invalid={Boolean(fieldErrors?.received_after)}
          aria-describedby={describedBy('received_after')}
        />
        {err('received_after')}
      </div>
      <div className="space-y-1.5">
        <Label htmlFor={`${id}-before`}>Received before</Label>
        <Input
          id={`${id}-before`}
          type="datetime-local"
          value={before}
          onChange={(e) => setBefore(e.target.value)}
          aria-invalid={Boolean(fieldErrors?.received_before)}
          aria-describedby={describedBy('received_before')}
        />
        {err('received_before')}
      </div>
      <div className="space-y-1.5">
        <Label htmlFor={`${id}-ct`}>Content type starts with</Label>
        <Input
          id={`${id}-ct`}
          value={contentType}
          maxLength={100}
          placeholder="application/json"
          onChange={(e) => setContentType(e.target.value)}
          aria-invalid={Boolean(fieldErrors?.content_type)}
          aria-describedby={describedBy('content_type')}
        />
        {err('content_type')}
      </div>
      <div className="space-y-1.5">
        <Label htmlFor={`${id}-sort`}>Order</Label>
        <select
          id={`${id}-sort`}
          className={selectClass}
          value={sort}
          onChange={(e) => setSort(e.target.value === 'asc' ? 'asc' : 'desc')}
        >
          <option value="desc">Newest first</option>
          <option value="asc">Oldest first</option>
        </select>
      </div>

      <div className="flex flex-wrap gap-2 sm:col-span-2 lg:col-span-4">
        <Button type="submit">Apply filters</Button>
        <Button type="button" variant="ghost" onClick={onClear}>
          Clear filters
        </Button>
      </div>
    </form>
  )
}
