import { render } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'

import oxlintConfig from '../../../../.oxlintrc.json?raw'

import { JsonTree } from '@/features/events/payload/JsonTree'
import { RawText } from '@/features/events/payload/RawText'

const payloads = [
  '<img src=x onerror=alert(1)>',
  '<script>alert(1)</script>',
  'javascript:alert(1)',
  '<svg onload=alert(1)></svg>',
  '<a href="javascript:alert(1)">click</a>',
  '<iframe src="https://evil.example"></iframe>',
]

/** No received content may become an element: only the app's own icons (svg with aria-hidden). */
function assertInert(container: HTMLElement) {
  expect(container.querySelector('img, script, iframe, a, object, embed, form, input')).toBeNull()
  for (const svg of container.querySelectorAll('svg')) expect(svg).toHaveAttribute('aria-hidden', 'true')
  for (const el of container.querySelectorAll('*')) {
    for (const attr of el.getAttributeNames()) expect(attr.startsWith('on')).toBe(false)
  }
}

describe('rendering untrusted content', () => {
  it('keeps dangerouslySetInnerHTML banned by lint', () => {
    const config = JSON.parse(oxlintConfig) as { rules: Record<string, unknown> }
    expect(config.rules['react/no-danger']).toBe('error')
    expect(config.rules['react/jsx-no-script-url']).toBe('error')
  })

  it('shows HTML, scripts, and javascript: URLs in the JSON tree as text', async () => {
    const alert = vi.spyOn(window, 'alert').mockImplementation(() => {})
    const value = Object.fromEntries(payloads.map((p, i) => [p, [p, { [`k${i}`]: p }]]))
    const { container, getAllByText } = render(<JsonTree value={value} />)
    await userEvent.click(getAllByText('Expand all')[0]!)
    for (const p of payloads) expect(getAllByText(JSON.stringify(p)).length).toBeGreaterThan(0)
    assertInert(container)
    expect(alert).not.toHaveBeenCalled()
  })

  it('shows them in the raw view as text', () => {
    const alert = vi.spyOn(window, 'alert').mockImplementation(() => {})
    const text = payloads.join('\n')
    const { container, getByLabelText } = render(<RawText text={text} />)
    expect(getByLabelText('Raw body').textContent).toBe(text)
    assertInert(container)
    expect(alert).not.toHaveBeenCalled()
  })
})
