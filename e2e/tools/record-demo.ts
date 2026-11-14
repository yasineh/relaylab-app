/**
 * Records the product walkthrough (docs/demo/walkthrough.md) as a 1080p WebM
 * video by driving the real application in Chromium. Everything on screen is
 * real RelayLab UI: a fresh demo workspace with synthetic seed data, and the
 * webhook sent by scripts/demo-send.sh while the recording runs. Overlays add
 * captions, a visible pointer, and a terminal panel that prints the command
 * being run and its real output.
 *
 *   docker compose -f docker-compose.prod.yml up -d --build --wait
 *   pnpm --filter @relaylab/e2e record-demo
 *
 * RELAYLAB_DEMO_URL picks the instance (default: the production-mode local
 * stack at https://localhost:8443). Output: e2e/recordings/relaylab-demo.webm
 *
 * Frames come from the Chrome DevTools screencast at 1920x1080 device pixels
 * and are encoded to VP9 with Playwright's bundled ffmpeg, so no extra tools
 * are needed.
 */
import { spawn, execFileSync } from 'node:child_process'
import { existsSync, mkdirSync, readdirSync } from 'node:fs'
import { homedir, platform } from 'node:os'
import { join } from 'node:path'

import { chromium, type Locator } from '@playwright/test'

const BASE = (process.env.RELAYLAB_DEMO_URL ?? 'https://localhost:8443').replace(/\/$/, '')
const LOCAL_TLS = new URL(BASE).hostname === 'localhost'
const ROOT = new URL('../../', import.meta.url).pathname
const OUT_DIR = join(ROOT, 'e2e/recordings')
const OUT = join(OUT_DIR, 'relaylab-demo.webm')
const FPS = 30
// CSS viewport 1536x864 at 1.25x = 1920x1080 device pixels: full HD, with UI
// text a quarter larger than at 1x so it stays legible on a laptop screen.
const VIEWPORT = { width: 1536, height: 864 }
const SCALE = 1.25

function ffmpegPath(): string {
  const cache =
    process.env.PLAYWRIGHT_BROWSERS_PATH ??
    (platform() === 'darwin'
      ? join(homedir(), 'Library/Caches/ms-playwright')
      : join(homedir(), '.cache/ms-playwright'))
  const dir = readdirSync(cache).find((d) => d.startsWith('ffmpeg'))
  if (!dir) throw new Error(`Playwright's ffmpeg is missing in ${cache}: run npx playwright install ffmpeg`)
  const bin = readdirSync(join(cache, dir)).find((f) => f.startsWith('ffmpeg'))
  return join(cache, dir, bin ?? 'ffmpeg')
}

/** Overlay helpers, installed in every document before the app's scripts. */
function overlays() {
  const css = `
    #demo-caption{position:fixed;left:50%;bottom:28px;transform:translateX(-50%);z-index:2147483646;
      max-width:min(1100px,90vw);padding:12px 22px;border-radius:10px;background:rgba(15,23,42,.92);color:#f8fafc;
      font:500 19px/1.45 ui-sans-serif,system-ui,sans-serif;text-align:center;box-shadow:0 8px 30px rgba(0,0,0,.25);
      transition:opacity .35s;opacity:0;pointer-events:none}
    #demo-caption.on{opacity:1}
    #demo-pointer{position:fixed;left:0;top:0;width:22px;height:22px;margin:-11px 0 0 -11px;border-radius:50%;
      z-index:2147483647;background:rgba(14,165,233,.35);border:2px solid #0284c7;pointer-events:none;
      transition:transform .12s}
    #demo-pointer.down{transform:scale(.7);background:rgba(14,165,233,.6)}
    #demo-terminal{position:fixed;right:40px;top:340px;width:820px;z-index:2147483645;border-radius:10px;overflow:hidden;
      background:#0f172a;color:#e2e8f0;box-shadow:0 18px 50px rgba(0,0,0,.35);font:14px/1.6 ui-monospace,SFMono-Regular,Menlo,monospace;
      transition:opacity .3s;opacity:0;pointer-events:none}
    #demo-terminal.on{opacity:1}
    #demo-terminal .bar{padding:8px 14px;background:#1e293b;color:#94a3b8;font:500 13px ui-sans-serif,system-ui,sans-serif}
    #demo-terminal pre{margin:0;padding:14px 16px;white-space:pre-wrap;overflow-wrap:anywhere}
    #demo-terminal .cmd{color:#7dd3fc}
    #demo-title{position:fixed;inset:0;z-index:2147483644;display:flex;flex-direction:column;align-items:center;justify-content:center;
      gap:14px;background:#0f172a;color:#f8fafc;font-family:ui-sans-serif,system-ui,sans-serif;transition:opacity .5s}
    #demo-title h1{font-size:56px;font-weight:650;letter-spacing:-.02em;margin:0}
    #demo-title p{font-size:22px;color:#cbd5e1;margin:0;max-width:900px;text-align:center;line-height:1.5}
  `
  const ready = () => {
    if (document.getElementById('demo-style')) return
    const style = document.createElement('style')
    style.id = 'demo-style'
    style.textContent = css
    document.head.append(style)
    const captionEl = Object.assign(document.createElement('div'), { id: 'demo-caption' })
    const pointer = Object.assign(document.createElement('div'), { id: 'demo-pointer' })
    const terminalEl = Object.assign(document.createElement('div'), { id: 'demo-terminal' })
    terminalEl.innerHTML = '<div class="bar">Terminal</div><pre></pre>'
    document.body.append(captionEl, pointer, terminalEl)
    const s = sessionStorage
    const restore = () => {
      const c = s.getItem('demo-caption')
      if (c) {
        captionEl.textContent = c
        captionEl.classList.add('on')
      }
      const p = s.getItem('demo-pointer')
      if (p) {
        const [x, y] = p.split(',')
        pointer.style.left = `${x}px`
        pointer.style.top = `${y}px`
      }
    }
    restore()
    document.addEventListener(
      'mousemove',
      (e) => {
        pointer.style.left = `${e.clientX}px`
        pointer.style.top = `${e.clientY}px`
        s.setItem('demo-pointer', `${e.clientX},${e.clientY}`)
      },
      true,
    )
    document.addEventListener('mousedown', () => pointer.classList.add('down'), true)
    document.addEventListener('mouseup', () => pointer.classList.remove('down'), true)
  }
  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', ready)
  else ready()
}

const browser = await chromium.launch()
const context = await browser.newContext({
  baseURL: BASE,
  viewport: VIEWPORT,
  deviceScaleFactor: SCALE,
  colorScheme: 'light',
  locale: 'en-US',
  timezoneId: 'UTC',
  ignoreHTTPSErrors: LOCAL_TLS,
})
await context.addInitScript(overlays)
const page = await context.newPage()

// --- Recorder: DevTools screencast frames -> constant-rate VP9 ------------
mkdirSync(OUT_DIR, { recursive: true })
const ffmpeg = spawn(
  ffmpegPath(),
  [
    '-loglevel',
    'error',
    '-y',
    '-f',
    'image2pipe',
    '-c:v',
    'mjpeg',
    '-framerate',
    String(FPS),
    '-i',
    'pipe:0',
    '-vf',
    'scale=1920:1080:force_original_aspect_ratio=decrease,pad=1920:1080:(ow-iw)/2:(oh-ih)/2:color=white',
    '-c:v',
    'libvpx-vp9',
    '-crf',
    '24',
    '-b:v',
    '0',
    '-deadline',
    'good',
    '-cpu-used',
    '4',
    '-row-mt',
    '1',
    '-pix_fmt',
    'yuv420p',
    OUT,
  ],
  { stdio: ['pipe', 'inherit', 'inherit'] },
)
const cdp = await context.newCDPSession(page)
let last: Buffer | null = null
cdp.on('Page.screencastFrame', (f: { data: string; sessionId: number }) => {
  last = Buffer.from(f.data, 'base64')
  void cdp.send('Page.screencastFrameAck', { sessionId: f.sessionId }).catch(() => {})
})
await cdp.send('Page.startScreencast', { format: 'jpeg', quality: 95, maxWidth: 1920, maxHeight: 1080 })
const started = Date.now()
let written = 0
const ticker = setInterval(() => {
  const due = Math.floor(((Date.now() - started) / 1000) * FPS)
  const frame = last
  if (!frame) return
  for (; written < due; written++) ffmpeg.stdin.write(frame)
}, 1000 / FPS)

// --- Helpers ----------------------------------------------------------------
const pause = (s: number) => page.waitForTimeout(s * 1000)

async function caption(text: string | null, hold = 0) {
  await page.evaluate((t) => {
    const el = document.getElementById('demo-caption')
    if (!el) return
    if (t) {
      el.textContent = t
      el.classList.add('on')
      sessionStorage.setItem('demo-caption', t)
    } else {
      el.classList.remove('on')
      sessionStorage.removeItem('demo-caption')
    }
  }, text)
  if (hold) await pause(hold)
}

async function pointAt(target: Locator) {
  await target.scrollIntoViewIfNeeded()
  const box = await target.boundingBox()
  if (!box) throw new Error(`not visible: ${target}`)
  await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2, { steps: 30 })
  await pause(0.4)
}

async function click(target: Locator, after = 1) {
  await pointAt(target)
  await target.click()
  await pause(after)
}

/** Smoothly scrolls an element to the middle of the screen, clear of the caption. */
async function center(target: Locator, seconds = 1.2) {
  await target.evaluate((el) => el.scrollIntoView({ behavior: 'smooth', block: 'center' }))
  await pause(seconds)
}

async function scrollBy(dy: number, seconds = 1.2) {
  const steps = Math.round(seconds * 30)
  for (let i = 0; i < steps; i++) {
    await page.mouse.wheel(0, dy / steps)
    await page.waitForTimeout(1000 / 30)
  }
}

async function terminal(lines: { cmd?: string; out?: string }[] | null) {
  await page.evaluate((ls) => {
    const el = document.getElementById('demo-terminal')
    if (!el) return
    if (!ls) {
      el.classList.remove('on')
      return
    }
    const pre = el.querySelector('pre')!
    pre.textContent = ''
    for (const l of ls) {
      if (l.cmd) {
        const span = document.createElement('span')
        span.className = 'cmd'
        span.textContent = `$ ${l.cmd}\n`
        pre.append(span)
      }
      if (l.out) pre.append(document.createTextNode(`${l.out}\n`))
    }
    el.classList.add('on')
  }, lines)
}

async function title(heading: string, text: string, seconds: number) {
  await page.evaluate(
    ([h, t]) => {
      const el = document.createElement('div')
      el.id = 'demo-title'
      el.innerHTML = '<h1></h1><p></p>'
      el.querySelector('h1')!.textContent = h
      el.querySelector('p')!.textContent = t
      document.body.append(el)
    },
    [heading, text] as const,
  )
  await pause(seconds)
  await page.evaluate(() => {
    const el = document.getElementById('demo-title')
    if (!el) return
    el.style.opacity = '0'
    setTimeout(() => el.remove(), 600)
  })
  await pause(0.8)
}

// --- The walkthrough (docs/demo/walkthrough.md) ------------------------------
try {
  // Scene 1: the landing page, signed out.
  await page.goto('/')
  await page.getByRole('heading', { level: 1 }).waitFor()
  await page.evaluate(() => document.fonts.ready)
  await page.mouse.move(VIEWPORT.width / 2, VIEWPORT.height - 120)
  await title(
    'RelayLab',
    'Receive a webhook, inspect every byte, and replay it. A walkthrough with synthetic data.',
    4,
  )
  await caption('RelayLab gives you a URL that records every request sent to it.', 4.5)
  await scrollBy(700, 2)
  await caption('Receive, inspect, and replay: the whole workflow fits in one screen.', 4)
  await scrollBy(-700, 1.5)

  // Scene 2: start a demo workspace.
  await caption(
    '“Try the demo” starts a temporary workspace with synthetic data. It is deleted after 24 hours.',
  )
  await pause(2)
  await click(page.getByRole('button', { name: 'Try the demo' }), 0)
  await page.waitForURL(/\/projects\/[^/]+\/events/)
  await page.getByRole('table', { name: 'Received events' }).waitFor()
  await pause(1.5)
  await caption('The demo project already has a few sample webhooks, with method, path, size, and time.', 5)

  // Scene 3: the project and its endpoint.
  const projectNav = page.getByRole('navigation', { name: 'Project' })
  await click(projectNav.getByRole('link', { name: 'Overview' }), 1.5)
  await caption('A project groups endpoints, events, and replays.', 4)
  await click(projectNav.getByRole('link', { name: 'Endpoints' }), 1.5)
  await caption('This project has two endpoints: one to receive webhooks, and a test sink to replay to.', 4.5)
  await click(page.getByRole('link', { name: 'Demo endpoint' }).first(), 1.5)
  const urlRegion = page.getByRole('region', { name: 'Receiving URL' })
  await urlRegion.waitFor()
  const endpointUrl = (await urlRegion.locator('code').first().innerText()).trim()
  await pointAt(urlRegion.locator('code').first())
  await caption('Each endpoint has its own unguessable receiving URL, with a ready-to-run curl example.', 5)
  await caption('Point a provider at this URL, or send a test request yourself.', 3.5)

  // Scene 4: send a webhook and watch it arrive live.
  await click(projectNav.getByRole('link', { name: 'Events' }), 1)
  await page.getByText('Live', { exact: true }).waitFor()
  await pointAt(page.getByText('Live', { exact: true }))
  await caption('The event list is live: new requests appear without a reload.', 3.5)
  const command = `scripts/demo-send.sh${LOCAL_TLS ? ' -k' : ''} ${endpointUrl}`
  await caption('Now send a synthetic “order shipped” webhook from a terminal.')
  await terminal([{ cmd: command }])
  await pause(3)
  const args = [...(LOCAL_TLS ? ['-k'] : []), endpointUrl]
  const output = execFileSync(join(ROOT, 'scripts/demo-send.sh'), args, { encoding: 'utf8' }).trim()
  await terminal([{ cmd: command }, { out: output }])
  const row = page
    .getByRole('table', { name: 'Received events' })
    .getByRole('row')
    .filter({ hasText: '/orders/shipped' })
  await row.waitFor({ timeout: 10_000 })
  await caption('RelayLab stores the request before it answers, and it appears in the list right away.', 4.5)
  await terminal(null)
  await pointAt(row)
  await pause(1.5)

  // Scene 5: inspect it.
  await click(row.getByRole('link', { name: /\/orders\/shipped/ }), 1.5)
  await caption('The overview shows the method, path, content type, size, and when it arrived.', 5)
  const tabs = page.getByRole('tablist', { name: 'Event details' })
  const panel = page.getByRole('tabpanel')
  await click(tabs.getByRole('tab', { name: /^Headers/ }), 0.5)
  const auth = panel.getByRole('row', { name: /^Authorization/ })
  await center(auth)
  await pointAt(auth)
  await caption('Every header as received. Authorization was removed on arrival and was never stored.', 6)
  await center(tabs)
  await click(tabs.getByRole('tab', { name: /^Query/ }), 0.5)
  await center(panel.getByRole('table', { name: 'Query parameters' }))
  await caption('Query parameters are listed one by one, next to the raw query string.', 4.5)
  await center(tabs)
  await click(tabs.getByRole('tab', { name: /^Body/ }), 0.5)
  await scrollBy(450, 1.2)
  await caption('The JSON body as a tree you can expand. The Raw view shows the exact bytes.', 6)
  await center(tabs)
  await click(tabs.getByRole('tab', { name: 'Raw request' }), 0.5)
  await scrollBy(300, 1)
  await caption('The whole request as it arrived, ready to copy, or to copy as a curl command.', 4.5)
  await page.evaluate(() => window.scrollTo({ top: 0, behavior: 'smooth' }))
  await pause(1.2)

  // Scene 6: replay to the test sink.
  await caption('Now replay it.')
  await click(page.getByRole('button', { name: 'Replay', exact: true }), 1.5)
  const dialog = page.getByRole('dialog', { name: 'Replay event' })
  await dialog.waitFor()
  await caption('Before sending, you see the destination and exactly which headers will go out.', 2)
  await center(dialog.getByText('Request preview'), 1.5)
  await pause(3)
  await caption(
    'This demo replays to its built-in test sink. Replays to URLs reach public HTTPS addresses only.',
    5,
  )
  await click(dialog.getByRole('button', { name: 'Send replay' }), 0.5)
  await dialog.waitFor({ state: 'hidden' })
  const replay = page.getByRole('list', { name: 'Replays of this event' }).getByRole('listitem').first()
  await replay.getByText(/Succeeded/).waitFor({ timeout: 15_000 })
  await pointAt(replay)
  await caption('A worker sends the replay in the background. It succeeded.', 4)

  // Scene 7: the replay result.
  await click(replay.getByRole('link', { name: /Details of replay/ }), 1.5)
  await page.getByRole('heading', { name: /^Response \d+/ }).waitFor()
  await caption('Replay history keeps the destination, timing, response status, headers, and body.', 6)
  await scrollBy(300, 1.2)
  await pause(3)
  await caption(null)
  await title('RelayLab', 'Recorded on the production-mode stack. All data shown is synthetic.', 4)
} finally {
  clearInterval(ticker)
  await cdp.send('Page.stopScreencast').catch(() => {})
  ffmpeg.stdin.end()
  await new Promise((resolve) => ffmpeg.on('close', resolve))
  await browser.close()
}

if (!existsSync(OUT)) throw new Error('recording failed')
process.stdout.write(`recorded ${(written / FPS).toFixed(1)} s to ${OUT}\n`)
