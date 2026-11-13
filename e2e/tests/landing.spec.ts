import { expect, test } from '@playwright/test'

/**
 * The landing page lays out without horizontal scrolling from phone to wide
 * desktop, and keeps its calls to action visible (add-landing-page 3.3).
 * Screenshots are attached to the report for review.
 */
for (const width of [320, 375, 768, 1280, 1920]) {
  test(`landing page at ${width} px`, async ({ page }, info) => {
    await page.setViewportSize({ width, height: 900 })
    await page.goto('/')
    await expect(page.getByRole('heading', { level: 1 })).toBeVisible()
    const cta = page.getByRole('button', { name: 'Try the demo' })
    await expect(cta).toBeVisible()
    await expect(cta).toBeInViewport()
    await page.locator('img').last().scrollIntoViewIfNeeded()
    const overflow = await page.evaluate(
      () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
    )
    expect(overflow, 'horizontal overflow in px').toBeLessThanOrEqual(0)
    await info.attach(`landing-${width}`, {
      body: await page.screenshot({ fullPage: true }),
      contentType: 'image/png',
    })
  })
}

test('the HTML carries the title, description, and Open Graph tags', async ({ request }) => {
  const html = await (await request.get('/')).text()
  expect(html).toMatch(/<title>RelayLab: receive, inspect, and replay webhooks<\/title>/)
  for (const tag of [
    'name="description"',
    'property="og:title"',
    'property="og:description"',
    'property="og:image"',
  ]) {
    expect(html).toContain(tag)
  }
  const image = await request.get('/og-image.png')
  expect(image.status()).toBe(200)
  expect(image.headers()['content-type']).toContain('image/png')
})

test('keyboard: the skip link comes first and every control shows focus', async ({ page }) => {
  await page.goto('/')
  await expect(page.getByRole('button', { name: 'Try the demo' })).toBeVisible()
  await page.keyboard.press('Tab')
  await expect(page.getByRole('link', { name: 'Skip to content' })).toBeFocused()
  for (let i = 0; i < 8; i++) {
    await page.keyboard.press('Tab')
    const outline = await page.evaluate(() => {
      const el = document.activeElement as HTMLElement
      const s = getComputedStyle(el)
      return { tag: el.tagName, ring: s.outlineStyle !== 'none' || s.boxShadow !== 'none' }
    })
    expect(outline.ring, `${outline.tag} #${i + 2} has no visible focus`).toBe(true)
  }
})
