import { useId, useRef, type KeyboardEvent, type ReactNode } from 'react'

import { cn } from '@/lib/utils'

export type Tab = { id: string; label: ReactNode; panel: ReactNode }

/**
 * WAI-ARIA tabs: one tab stop for the tab list, arrow keys (and Home/End)
 * move between tabs and select them, and only the selected panel renders.
 */
export function Tabs({
  label,
  tabs,
  selected,
  onSelect,
}: {
  label: string
  tabs: Tab[]
  selected: string
  onSelect: (id: string) => void
}) {
  const base = useId()
  const refs = useRef<Record<string, HTMLButtonElement | null>>({})
  const current = tabs.find((t) => t.id === selected) ?? tabs[0]!

  function onKeyDown(e: KeyboardEvent) {
    const i = tabs.findIndex((t) => t.id === current.id)
    const next =
      e.key === 'ArrowRight'
        ? (i + 1) % tabs.length
        : e.key === 'ArrowLeft'
          ? (i - 1 + tabs.length) % tabs.length
          : e.key === 'Home'
            ? 0
            : e.key === 'End'
              ? tabs.length - 1
              : -1
    if (next < 0) return
    e.preventDefault()
    const t = tabs[next]!
    onSelect(t.id)
    refs.current[t.id]?.focus()
  }

  return (
    <div className="space-y-4">
      <div role="tablist" aria-label={label} className="flex flex-wrap gap-1 border-b">
        {tabs.map((t) => {
          const active = t.id === current.id
          return (
            <button
              key={t.id}
              ref={(el) => {
                refs.current[t.id] = el
              }}
              type="button"
              role="tab"
              id={`${base}-tab-${t.id}`}
              aria-selected={active}
              aria-controls={`${base}-panel-${t.id}`}
              tabIndex={active ? 0 : -1}
              onClick={() => onSelect(t.id)}
              onKeyDown={onKeyDown}
              className={cn(
                '-mb-px rounded-t-sm border-b-2 px-3 pb-2 text-sm font-medium transition-colors focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none',
                active
                  ? 'border-primary text-foreground'
                  : 'border-transparent text-muted-foreground hover:text-foreground',
              )}
            >
              {t.label}
            </button>
          )
        })}
      </div>
      <div
        role="tabpanel"
        id={`${base}-panel-${current.id}`}
        aria-labelledby={`${base}-tab-${current.id}`}
        // The panel is focusable so keyboard users can reach content without interactive children.
        tabIndex={0}
        className="focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none"
      >
        {current.panel}
      </div>
    </div>
  )
}
