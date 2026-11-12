/**
 * A controllable EventSource for tests (jsdom has none). setup.ts installs it
 * globally; tests drive connections with open(), emit(), and fail().
 */
export class FakeEventSource extends EventTarget {
  static readonly CONNECTING = 0
  static readonly OPEN = 1
  static readonly CLOSED = 2
  static instances: FakeEventSource[] = []

  /** The most recently created connection. */
  static last(): FakeEventSource {
    const es = FakeEventSource.instances.at(-1)
    if (!es) throw new Error('no EventSource was created')
    return es
  }

  /** Connections not closed by the code under test. */
  static open(): FakeEventSource[] {
    return FakeEventSource.instances.filter((e) => e.readyState !== FakeEventSource.CLOSED)
  }

  readonly url: string
  readonly withCredentials: boolean
  readyState = FakeEventSource.CONNECTING
  onopen: ((e: Event) => void) | null = null
  onerror: ((e: Event) => void) | null = null
  onmessage: ((e: MessageEvent) => void) | null = null

  constructor(url: string | URL, init?: EventSourceInit) {
    super()
    this.url = String(url)
    this.withCredentials = Boolean(init?.withCredentials)
    FakeEventSource.instances.push(this)
  }

  close() {
    this.readyState = FakeEventSource.CLOSED
  }

  /** The server accepted the connection. */
  open() {
    this.readyState = FakeEventSource.OPEN
    this.onopen?.(new Event('open'))
  }

  /** The server sent a named event. */
  emit(type: string, data: unknown, lastEventId = '') {
    if (this.readyState === FakeEventSource.CLOSED) return
    this.dispatchEvent(new MessageEvent(type, { data: JSON.stringify(data), lastEventId }))
  }

  /** The connection failed or dropped. */
  fail() {
    if (this.readyState === FakeEventSource.CLOSED) return
    this.readyState = FakeEventSource.CONNECTING
    this.onerror?.(new Event('error'))
  }
}
