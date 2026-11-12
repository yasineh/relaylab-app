import { Lock } from 'lucide-react'

/** The explanation shown wherever removed values appear. */
export const REDACTION_NOTE =
  'RelayLab does not store credential values. These values were present in the request, but were discarded when it arrived.'

/** Marks a value removed at receipt: icon plus text, never colour alone. */
export function RedactedMarker({ describedBy }: { describedBy?: string }) {
  return (
    <span
      className="inline-flex items-center gap-1 rounded bg-muted px-1.5 py-0.5 font-sans text-xs font-medium text-foreground"
      aria-describedby={describedBy}
    >
      <Lock aria-hidden="true" className="size-3.5" />
      Removed at receipt
    </span>
  )
}
