/** Formats an ISO timestamp (stored in UTC) as a date in the viewer's locale and time zone. */
export function formatDate(iso: string): string {
  return new Intl.DateTimeFormat(undefined, { dateStyle: 'medium' }).format(new Date(iso))
}

/** Formats an ISO timestamp as date and time in the viewer's locale and time zone. */
export function formatDateTime(iso: string): string {
  return new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'short' }).format(new Date(iso))
}

const RELATIVE_STEPS: [Intl.RelativeTimeFormatUnit, number][] = [
  ['second', 60],
  ['minute', 60],
  ['hour', 24],
  ['day', 30],
  ['month', 12],
  ['year', Infinity],
]

/** Formats an ISO timestamp relative to `now` ("3 minutes ago") in the viewer's locale. */
export function formatRelative(iso: string, now: Date = new Date()): string {
  let value = (new Date(iso).getTime() - now.getTime()) / 1000
  for (const [unit, size] of RELATIVE_STEPS) {
    if (Math.abs(value) < size) {
      return new Intl.RelativeTimeFormat(undefined, { numeric: 'auto' }).format(Math.round(value), unit)
    }
    value /= size
  }
  return formatDateTime(iso)
}

/** Formats an ISO timestamp with seconds, for precise event times. */
export function formatPreciseDateTime(iso: string): string {
  return new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'medium' }).format(
    new Date(iso),
  )
}

/** Formats a retention period in seconds as "24 hours" or "7 days" (days from 2 whole days up). */
export function formatRetention(seconds: number): string {
  const day = 86_400
  if (seconds >= 2 * day && seconds % day === 0) return `${seconds / day} days`
  const h = Math.max(1, Math.round(seconds / 3600))
  return `${h} ${h === 1 ? 'hour' : 'hours'}`
}
