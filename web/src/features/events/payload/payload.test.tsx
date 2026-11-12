import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'

import { parseJson, visibleRows, type Json } from '@/features/events/payload/json-rows'
import { JsonTree, VIRTUALIZE_ABOVE } from '@/features/events/payload/JsonTree'

const nested = {
  a: { b: { c: { d: { e: 1 } } } },
  list: [1, 'two', null, true],
  empty: {},
  none: [],
} satisfies Json

describe('visibleRows', () => {
  it('opens containers above depth 3 and keeps key order', () => {
    const rows = visibleRows({ z: 1, a: { b: { c: { d: 1 } } } }, new Map())
    const opens = rows.filter((r) => r.kind === 'open').map((r) => [r.path, r.expanded])
    expect(opens).toEqual([
      ['$', true],
      ['$/a', true],
      ['$/a/b', true],
      ['$/a/b/c', false], // depth 3: collapsed by default
    ])
    expect(rows[1]).toMatchObject({ kind: 'value', label: 'z' })
  })

  it('respects user overrides and escapes keys in paths', () => {
    const v = { 'x/y': { deep: 1 }, x: { y: { deep: 2 } } }
    const closed = visibleRows(v, new Map([['$/x~1y', false]]))
    expect(closed.find((r) => r.path === '$/x~1y')).toMatchObject({ expanded: false })
    expect(closed.find((r) => r.path === '$/x/y')).toMatchObject({ expanded: true })
  })

  it('handles nesting far deeper than the call stack would allow', () => {
    let deep: Json = 0
    for (let i = 0; i < 20000; i++) deep = [deep]
    const all = new Map<string, boolean>()
    let p = '$'
    for (let i = 0; i < 20000; i++) {
      all.set(p, true)
      p += '/0'
    }
    expect(visibleRows(deep, all)).toHaveLength(20000 * 2 + 1)
  })

  it('reports invalid JSON instead of throwing', () => {
    expect(parseJson('{"a":')).toEqual({ ok: false })
    expect(parseJson('[1]')).toEqual({ ok: true, value: [1] })
  })
})

describe('JsonTree', () => {
  it('renders values as text and toggles containers', async () => {
    render(<JsonTree value={nested} />)
    const tree = screen.getByRole('list', { name: 'JSON body' })
    expect(within(tree).getByText('"two"')).toBeInTheDocument()
    expect(within(tree).getByText('null')).toBeInTheDocument()
    // a > b > c is depth 3, so c is collapsed and d is not shown.
    const c = within(tree).getByRole('button', { name: /"c"/ })
    expect(c).toHaveAttribute('aria-expanded', 'false')
    expect(within(tree).queryByText('"d"')).not.toBeInTheDocument()
    await userEvent.click(c)
    expect(within(tree).getByRole('button', { name: /"c"/ })).toHaveAttribute('aria-expanded', 'true')
    expect(within(tree).getByText('"d"')).toBeInTheDocument()

    await userEvent.click(within(tree).getByRole('button', { name: /"list"/ }))
    expect(within(tree).queryByText('"two"')).not.toBeInTheDocument()
  })

  it('expands and collapses everything', async () => {
    render(<JsonTree value={nested} />)
    await userEvent.click(screen.getByRole('button', { name: 'Expand all' }))
    expect(screen.getByText('"e"')).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Collapse all' }))
    expect(screen.queryByText('"a"')).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: /4 keys/ })).toHaveAttribute('aria-expanded', 'false')
  })

  it('truncates long strings until asked', async () => {
    render(<JsonTree value={{ s: 'x'.repeat(2000) }} />)
    expect(screen.queryByText(`"${'x'.repeat(2000)}"`)).not.toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: /Show 1,502 more characters/ }))
    expect(screen.getByText(`"${'x'.repeat(2000)}"`)).toBeInTheDocument()
  })

  it('renders a 1 MiB document with only a window of rows', () => {
    const items = Array.from({ length: 9000 }, (_, i) => ({
      id: i,
      name: `item-${i}`,
      tags: ['a', 'b'],
      deep: { x: { y: 1 } },
    }))
    const text = JSON.stringify({
      items,
      pad: 'p'.repeat(Math.max(0, (1 << 20) - JSON.stringify({ items }).length - 20)),
    })
    expect(text.length).toBeGreaterThanOrEqual((1 << 20) - 64)
    const parsed = parseJson(text)
    if (!parsed.ok) throw new Error('fixture must be valid JSON')

    // jsdom has no layout: give the scroll container a viewport-sized client
    // box (what the virtualizer reads) so it has rows to fill.
    vi.spyOn(HTMLElement.prototype, 'clientHeight', 'get').mockReturnValue(600)
    vi.spyOn(HTMLElement.prototype, 'clientWidth', 'get').mockReturnValue(800)
    // ...and rows a real line height (measureElement reads offsetHeight).
    vi.spyOn(HTMLElement.prototype, 'offsetHeight', 'get').mockReturnValue(24)
    const start = performance.now()
    render(<JsonTree value={parsed.value} />)
    expect(performance.now() - start).toBeLessThan(5000)

    const region = screen.getByRole('region', { name: /JSON body \(.*lines\)/ })
    const rendered = within(region).getAllByRole('listitem').length
    expect(rendered).toBeGreaterThan(0)
    expect(rendered).toBeLessThan(VIRTUALIZE_ABOVE)
    // root (0) > items (1) > items[0] (2) are open; items[0].deep (3) is collapsed.
    expect(within(region).getAllByRole('button', { name: /4 keys/ })[0]).toHaveAttribute(
      'aria-expanded',
      'true',
    )
    expect(within(region).getAllByRole('button', { name: /"deep"/ })[0]).toHaveAttribute(
      'aria-expanded',
      'false',
    )
  })
})
