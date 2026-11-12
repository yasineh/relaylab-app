import { useVirtualizer } from '@tanstack/react-virtual'
import { ChevronDown, ChevronRight } from 'lucide-react'
import { useMemo, useRef, useState } from 'react'

import { Button } from '@/components/ui/button'
import { VisibleText } from '@/features/events/payload/RawText'
import {
  DEFAULT_OPEN_DEPTH,
  STRING_PREVIEW_CHARS,
  visibleRows,
  type Json,
  type OpenMode,
  type Row,
} from '@/features/events/payload/json-rows'

/** Above this many visible rows, only the rows in view are rendered. */
export const VIRTUALIZE_ABOVE = 300

const ROW_HEIGHT = 24
const INDENT_REM = 1.25
/** Before the first measurement, assume a screenful so rows render at once. */
const INITIAL_RECT = { width: 800, height: 600 }

/**
 * Collapsible JSON tree. Every received value is rendered as a React text
 * node: nothing is interpreted as markup, linked, or used in attributes.
 */
export function JsonTree({ value, label = 'JSON body' }: { value: Json; label?: string }) {
  const [overrides, setOverrides] = useState<ReadonlyMap<string, boolean>>(() => new Map())
  const [mode, setMode] = useState<OpenMode>('depth')
  const [openStrings, setOpenStrings] = useState<ReadonlySet<string>>(() => new Set())
  const rows = useMemo(() => visibleRows(value, overrides, mode), [value, overrides, mode])

  const toggle = (path: string, expanded: boolean) => setOverrides((m) => new Map(m).set(path, !expanded))
  const showString = (path: string) => setOpenStrings((s) => new Set(s).add(path))

  // Switching mode clears individual toggles, so every container follows it.
  const setAll = (next: OpenMode) => {
    setMode(next)
    setOverrides(new Map())
  }

  return (
    <div className="space-y-2">
      <div className="flex flex-wrap gap-2">
        <Button variant="outline" size="sm" onClick={() => setAll('all')}>
          Expand all
        </Button>
        <Button variant="outline" size="sm" onClick={() => setAll('none')}>
          Collapse all
        </Button>
        <Button variant="ghost" size="sm" onClick={() => setAll('depth')}>
          Reset to depth {DEFAULT_OPEN_DEPTH}
        </Button>
      </div>
      {rows.length > VIRTUALIZE_ABOVE ? (
        <VirtualRows
          rows={rows}
          label={label}
          openStrings={openStrings}
          onToggle={toggle}
          onShowString={showString}
        />
      ) : (
        <ul
          aria-label={label}
          className="overflow-x-auto rounded-md border bg-muted/30 p-2 font-mono text-sm"
        >
          {rows.map((r) => (
            <li key={r.path}>
              <TreeRow row={r} openStrings={openStrings} onToggle={toggle} onShowString={showString} />
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}

type RowHandlers = {
  openStrings: ReadonlySet<string>
  onToggle: (path: string, expanded: boolean) => void
  onShowString: (path: string) => void
}

function VirtualRows({ rows, label, ...handlers }: { rows: Row[]; label: string } & RowHandlers) {
  const scrollRef = useRef<HTMLElement>(null)
  // oxlint-disable-next-line react/incompatible-library -- no React Compiler in this app; the virtualizer drives its own re-renders
  const virtualizer = useVirtualizer({
    count: rows.length,
    getScrollElement: () => scrollRef.current,
    estimateSize: () => ROW_HEIGHT,
    overscan: 20,
    getItemKey: (i) => rows[i]!.path,
    initialRect: INITIAL_RECT,
  })
  return (
    <section
      ref={scrollRef}
      className="max-h-[70vh] overflow-auto rounded-md border bg-muted/30 p-2 font-mono text-sm"
      // Keyboard users scroll it by moving focus through the toggle buttons.
      aria-label={`${label} (${rows.length.toLocaleString()} lines)`}
    >
      <ul aria-label={label} className="relative" style={{ height: virtualizer.getTotalSize() }}>
        {virtualizer.getVirtualItems().map((v) => (
          <li
            key={v.key}
            data-index={v.index}
            ref={virtualizer.measureElement}
            className="absolute inset-x-0"
            style={{ transform: `translateY(${v.start}px)` }}
          >
            <TreeRow row={rows[v.index]!} {...handlers} />
          </li>
        ))}
      </ul>
    </section>
  )
}

function TreeRow({ row, openStrings, onToggle, onShowString }: { row: Row } & RowHandlers) {
  const indent = { paddingLeft: `${row.depth * INDENT_REM}rem` }
  if (row.kind === 'close') {
    return (
      <div style={indent} className="text-muted-foreground">
        {row.container === 'array' ? ']' : '}'}
      </div>
    )
  }
  if (row.kind === 'open') {
    const [openCh, closeCh] = row.container === 'array' ? ['[', ']'] : ['{', '}']
    const count = `${row.size} ${row.container === 'array' ? (row.size === 1 ? 'item' : 'items') : row.size === 1 ? 'key' : 'keys'}`
    if (row.size === 0) {
      return (
        <div style={indent} className="min-h-6 py-0.5 break-all">
          <Key label={row.label} />
          <span className="text-muted-foreground">
            {openCh}
            {closeCh}
          </span>
        </div>
      )
    }
    return (
      <div style={indent} className="min-h-6 break-all">
        <button
          type="button"
          aria-expanded={row.expanded}
          onClick={() => onToggle(row.path, row.expanded)}
          className="inline-flex items-start gap-1 rounded-sm py-0.5 text-left hover:bg-muted focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none"
        >
          {row.expanded ? (
            <ChevronDown aria-hidden="true" className="mt-0.5 size-4 shrink-0" />
          ) : (
            <ChevronRight aria-hidden="true" className="mt-0.5 size-4 shrink-0" />
          )}
          <span>
            <Key label={row.label} />
            <span className="text-muted-foreground">
              {row.expanded ? openCh : `${openCh}…${closeCh}`} <span className="text-xs">{count}</span>
            </span>
          </span>
        </button>
      </div>
    )
  }
  return (
    <div style={indent} className="min-h-6 py-0.5 pl-5 break-all whitespace-pre-wrap">
      <Key label={row.label} />
      <Primitive
        value={row.value}
        expanded={openStrings.has(row.path)}
        onShowMore={() => onShowString(row.path)}
      />
    </div>
  )
}

function Key({ label }: { label: string | null }) {
  if (label === null) return null
  return (
    <>
      <span className="text-sky-700 dark:text-sky-300">
        <VisibleText text={JSON.stringify(label)} />
      </span>
      <span className="text-muted-foreground">: </span>
    </>
  )
}

function Primitive({
  value,
  expanded,
  onShowMore,
}: {
  value: null | boolean | number | string
  expanded: boolean
  onShowMore: () => void
}) {
  if (typeof value !== 'string') {
    return <span className="text-violet-700 dark:text-violet-300">{String(value)}</span>
  }
  // JSON.stringify shows the value exactly as JSON, escapes included.
  const full = JSON.stringify(value)
  if (expanded || full.length <= STRING_PREVIEW_CHARS) {
    return (
      <span className="text-emerald-700 dark:text-emerald-300">
        <VisibleText text={full} />
      </span>
    )
  }
  const hidden = full.length - STRING_PREVIEW_CHARS
  return (
    <span>
      <span className="text-emerald-700 dark:text-emerald-300">
        <VisibleText text={full.slice(0, STRING_PREVIEW_CHARS)} />…
      </span>{' '}
      <button
        type="button"
        onClick={onShowMore}
        className="rounded-sm font-sans text-xs underline underline-offset-4 focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none"
      >
        Show {hidden.toLocaleString()} more characters
      </button>
    </span>
  )
}
