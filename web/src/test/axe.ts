import axe from 'axe-core'

/**
 * Runs axe on `root` and returns violations as readable strings. jsdom has no
 * layout or rendering, so colour contrast is checked in the browser instead.
 */
export async function axeViolations(root: Element = document.body): Promise<string[]> {
  const results = await axe.run(root, { rules: { 'color-contrast': { enabled: false } } })
  return results.violations.flatMap((v) => v.nodes.map((n) => `${v.id}: ${v.help} (${n.target.join(' ')})`))
}
