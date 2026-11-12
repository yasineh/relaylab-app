import type { EventSummary } from '@/features/events/api'

/**
 * Connection state shown to the user (live-event-stream spec):
 * - connecting: the first connection has not opened yet
 * - live: connected; new events arrive as they are received
 * - reconnecting: the connection dropped and is being re-established
 * - offline: several attempts failed in a row; still retrying with backoff
 */
export type StreamState = 'connecting' | 'live' | 'reconnecting' | 'offline'

export type StreamHandlers = {
  onState: (state: StreamState) => void
  onEvent: (event: EventSummary) => void
  /**
   * A fresh connection (no resume point) has opened: the first one, or the
   * one after `resync.required`. The caller reloads its lists now: the
   * subscription already exists, so nothing committed afterwards is missed.
   */
  onSync: () => void
  /** The server ended the stream because the session is gone. */
  onSessionExpired: () => void
  /**
   * A connection attempt failed before opening: the server refused it (401,
   * 404, 429) or is unreachable. EventSource hides the status, so the caller
   * may check the session and project through the REST API.
   */
  onRejected: () => void
}

type Timers = {
  setTimeout: (fn: () => void, ms: number) => unknown
  clearTimeout: (id: unknown) => void
}

export type StreamClientOptions = {
  /** Injected in tests. */
  EventSourceImpl?: typeof EventSource
  timers?: Timers
}

/** First retry delay, matching the server's `retry: 3000`. */
export const RETRY_BASE_MS = 3_000
/** Retry delays double up to this cap (spec: backoff up to 30 seconds). */
export const RETRY_MAX_MS = 30_000
/**
 * This many consecutive failed attempts shows Offline. With the backoff that
 * is 9 s into an outage; it stays Offline until a connection opens.
 */
export const OFFLINE_AFTER_FAILURES = 3

export const streamUrl = (projectId: string) =>
  `/api/v1/projects/${encodeURIComponent(projectId)}/events/stream`

/**
 * One project's live event stream. It owns reconnection instead of relying
 * on EventSource's fixed-delay retry, so it can back off up to 30 s and
 * resume after server refusals: every reconnect passes the last received id
 * as `last_event_id`, and the server replays what was missed.
 */
export class EventStreamClient {
  private readonly url: string
  private readonly h: StreamHandlers
  private readonly ES: typeof EventSource
  private readonly t: Timers
  private es: EventSource | null = null
  private timer: unknown = null
  private lastId: string | null = null
  private attempt = 0 // consecutive failures since the last open
  private stopped = true
  private state: StreamState = 'connecting'

  constructor(url: string, handlers: StreamHandlers, opts: StreamClientOptions = {}) {
    this.url = url
    this.h = handlers
    this.ES = opts.EventSourceImpl ?? globalThis.EventSource
    this.t = opts.timers ?? {
      setTimeout: (fn, ms) => globalThis.setTimeout(fn, ms),
      clearTimeout: (id) => globalThis.clearTimeout(id as ReturnType<typeof setTimeout>),
    }
  }

  start() {
    if (!this.stopped) return
    this.stopped = false
    this.open()
  }

  stop() {
    this.stopped = true
    this.close()
    if (this.timer !== null) this.t.clearTimeout(this.timer)
    this.timer = null
  }

  /** Reconnect now instead of waiting for the backoff (for example when the browser comes back online). */
  retryNow() {
    if (this.stopped || this.es || this.timer === null) return
    this.t.clearTimeout(this.timer)
    this.timer = null
    this.open()
  }

  private setState(s: StreamState) {
    if (s === this.state) return
    this.state = s
    this.h.onState(s)
  }

  private close() {
    if (!this.es) return
    this.es.onopen = null
    this.es.onerror = null
    this.es.close()
    this.es = null
  }

  private open() {
    const fresh = this.lastId === null
    const url = fresh ? this.url : `${this.url}?last_event_id=${encodeURIComponent(this.lastId!)}`
    const es = new this.ES(url, { withCredentials: true })
    this.es = es
    let opened = false

    es.onopen = () => {
      opened = true
      this.attempt = 0
      this.setState('live')
      if (fresh) this.h.onSync()
    }
    es.onerror = () => {
      // Close and own the retry: EventSource would retry at a fixed delay,
      // and gives up entirely after a non-200 response.
      this.close()
      if (!opened) this.h.onRejected()
      this.scheduleRetry()
    }
    es.addEventListener('event.created', (e) => {
      const m = e as MessageEvent<string>
      let summary: EventSummary
      try {
        summary = JSON.parse(m.data) as EventSummary
      } catch {
        return
      }
      if (m.lastEventId) this.lastId = m.lastEventId
      this.h.onEvent(summary)
    })
    es.addEventListener('resync.required', () => {
      // Too much was missed (or the server lost track): start over without a
      // resume point; onSync reloads lists once the new stream is open.
      this.close()
      this.lastId = null
      this.open()
    })
    es.addEventListener('session.expired', () => {
      this.stop()
      this.h.onSessionExpired()
    })
  }

  private scheduleRetry() {
    if (this.stopped) return
    const delay = Math.min(RETRY_BASE_MS * 2 ** this.attempt, RETRY_MAX_MS)
    this.attempt++
    this.setState(this.attempt >= OFFLINE_AFTER_FAILURES ? 'offline' : 'reconnecting')
    this.timer = this.t.setTimeout(() => {
      this.timer = null
      this.open()
    }, delay)
  }
}
