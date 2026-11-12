/** A JSON value as produced by JSON.parse. */
export type Json = null | boolean | number | string | Json[] | { [key: string]: Json }

/** Containers deeper than this start collapsed (the root is depth 0). */
export const DEFAULT_OPEN_DEPTH = 3

/** Strings longer than this are truncated until the user expands them. */
export const STRING_PREVIEW_CHARS = 500

/** One visible line of the tree. `path` is a stable id for expand state. */
export type Row =
  | {
      kind: 'open'
      path: string
      depth: number
      label: string | null
      container: 'object' | 'array'
      size: number
      expanded: boolean
    }
  | { kind: 'close'; path: string; depth: number; container: 'object' | 'array' }
  | {
      kind: 'value'
      path: string
      depth: number
      label: string | null
      value: null | boolean | number | string
    }

/** Whether a container is open: the user's choice, else open above DEFAULT_OPEN_DEPTH. */
export const isExpanded = (path: string, depth: number, overrides: ReadonlyMap<string, boolean>) =>
  overrides.get(path) ?? depth < DEFAULT_OPEN_DEPTH

/**
 * Flattens a parsed JSON value into the rows currently visible, given which
 * containers the user opened or closed. Object keys keep their document order
 * (JSON.parse preserves it, except integer-like keys, which JavaScript sorts).
 */
export function visibleRows(root: Json, overrides: ReadonlyMap<string, boolean>): Row[] {
  const rows: Row[] = []
  // Explicit stack instead of recursion: nesting depth is attacker-controlled.
  type Frame =
    { value: Json; path: string; depth: number; label: string | null } | { closeOf: Row & { kind: 'open' } }
  const stack: Frame[] = [{ value: root, path: '$', depth: 0, label: null }]
  while (stack.length > 0) {
    const f = stack.pop()!
    if ('closeOf' in f) {
      rows.push({
        kind: 'close',
        path: f.closeOf.path + ':close',
        depth: f.closeOf.depth,
        container: f.closeOf.container,
      })
      continue
    }
    const { value, path, depth, label } = f
    if (value === null || typeof value !== 'object') {
      rows.push({ kind: 'value', path, depth, label, value })
      continue
    }
    const isArray = Array.isArray(value)
    const entries: [string, Json][] = isArray ? value.map((v, i) => [String(i), v]) : Object.entries(value)
    const open: Row & { kind: 'open' } = {
      kind: 'open',
      path,
      depth,
      label,
      container: isArray ? 'array' : 'object',
      size: entries.length,
      expanded: isExpanded(path, depth, overrides),
    }
    rows.push(open)
    if (!open.expanded || entries.length === 0) continue
    stack.push({ closeOf: open })
    for (let i = entries.length - 1; i >= 0; i--) {
      const [k, v] = entries[i]!
      stack.push({
        value: v,
        path: `${path}/${encodePathPart(k)}`,
        depth: depth + 1,
        label: isArray ? null : k,
      })
    }
  }
  return rows
}

/** Escapes "/" and "~" in keys so paths stay unique (JSON Pointer style). */
const encodePathPart = (k: string) => k.replaceAll('~', '~0').replaceAll('/', '~1')

/** Every container path, for "Expand all". Stops at `limit` to stay responsive. */
export function allContainerPaths(root: Json, limit = 5000): string[] {
  const out: string[] = []
  const stack: { value: Json; path: string }[] = [{ value: root, path: '$' }]
  while (stack.length > 0 && out.length < limit) {
    const { value, path } = stack.pop()!
    if (value === null || typeof value !== 'object') continue
    out.push(path)
    const entries = Array.isArray(value)
      ? value.map((v, i) => [String(i), v] as const)
      : Object.entries(value)
    for (const [k, v] of entries) stack.push({ value: v, path: `${path}/${encodePathPart(k)}` })
  }
  return out
}

/** Parses body text as JSON, or returns undefined when it is not valid JSON. */
export function parseJson(text: string): { ok: true; value: Json } | { ok: false } {
  try {
    return { ok: true, value: JSON.parse(text) as Json }
  } catch {
    return { ok: false }
  }
}
