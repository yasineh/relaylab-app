import { visibleSegments } from '@/features/events/payload/bytes'

/**
 * The exact body text, wrapped, with invisible and control characters shown
 * as escapes. Rendered as React text only.
 */
export function RawText({ text, label = 'Raw body' }: { text: string; label?: string }) {
  if (text === '') return <p className="text-sm text-muted-foreground">The body is empty.</p>
  return (
    <pre
      aria-label={label}
      className="max-h-[70vh] overflow-auto rounded-md border bg-muted/30 p-3 font-mono text-sm break-all whitespace-pre-wrap"
    >
      <VisibleText text={text} />
    </pre>
  )
}

/**
 * Text with control and bidirectional-override characters shown as
 * highlighted escapes, so received text cannot display differently from what
 * it contains. Used for every received string, in the raw view and the tree.
 */
export function VisibleText({ text }: { text: string }) {
  return (
    <>
      {visibleSegments(text).map((s, i) =>
        s.escape ? (
          <span
            key={i}
            className="rounded-sm bg-amber-200/60 px-0.5 text-amber-950 dark:bg-amber-500/30 dark:text-amber-100"
          >
            {s.text}
          </span>
        ) : (
          s.text
        ),
      )}
    </>
  )
}
