import { describe, expect, it, vi } from 'vitest'

import { summary } from '@/features/events/fixtures'
import {
  EventStreamClient,
  RETRY_MAX_MS,
  streamUrl,
  type StreamHandlers,
  type StreamState,
} from '@/features/events/stream'
import { FakeEventSource } from '@/test/event-source'

const URL = streamUrl('p1')

/** Manual clock and timers so backoff is deterministic. */
function manualTimers() {
  let now = 0
  const pending: { at: number; fn: () => void; id: number }[] = []
  let seq = 0
  return {
    timers: {
      setTimeout: (fn: () => void, ms: number) => {
        pending.push({ at: now + ms, fn, id: ++seq })
        return seq
      },
      clearTimeout: (id: unknown) => {
        const i = pending.findIndex((p) => p.id === id)
        if (i >= 0) pending.splice(i, 1)
      },
    },
    /** Delay of the only pending timer. */
    nextDelay: () => {
      expect(pending).toHaveLength(1)
      return pending[0]!.at - now
    },
    pendingCount: () => pending.length,
    advance: (ms: number) => {
      now += ms
      for (const due of pending.filter((t) => t.at <= now)) {
        pending.splice(pending.indexOf(due), 1)
        due.fn()
      }
    },
  }
}

function setup() {
  const clock = manualTimers()
  const states: StreamState[] = []
  const h = {
    onState: vi.fn<StreamHandlers['onState']>((s) => void states.push(s)),
    onEvent: vi.fn<StreamHandlers['onEvent']>(),
    onSync: vi.fn<() => void>(),
    onSessionExpired: vi.fn<() => void>(),
    onRejected: vi.fn<() => void>(),
  } satisfies StreamHandlers
  const client = new EventStreamClient(URL, h, {
    EventSourceImpl: FakeEventSource as unknown as typeof EventSource,
    timers: clock.timers,
  })
  return { client, h, states, clock }
}

describe('EventStreamClient (3.1)', () => {
  it('opens with credentials, goes live, and syncs once on a fresh connection', () => {
    const { client, h, states } = setup()
    client.start()
    const es = FakeEventSource.last()
    expect(es.url).toBe('/api/v1/projects/p1/events/stream')
    expect(es.withCredentials).toBe(true)
    expect(h.onSync).not.toHaveBeenCalled()
    es.open()
    expect(states).toEqual(['live'])
    expect(h.onSync).toHaveBeenCalledOnce()
  })

  it('delivers event.created summaries and ignores malformed data', () => {
    const { client, h } = setup()
    client.start()
    const es = FakeEventSource.last()
    es.open()
    es.emit('event.created', summary({ id: 'e1' }), 'e1')
    es.dispatchEvent(new MessageEvent('event.created', { data: '{not json', lastEventId: 'bad' }))
    expect(h.onEvent).toHaveBeenCalledTimes(1)
    expect(h.onEvent).toHaveBeenCalledWith(summary({ id: 'e1' }))
  })

  it('reconnects after a drop and resumes from the last received id without resyncing', () => {
    const { client, h, states, clock } = setup()
    client.start()
    const first = FakeEventSource.last()
    first.open()
    first.emit('event.created', summary({ id: 'e9' }), 'e9')
    first.fail()
    expect(first.readyState).toBe(FakeEventSource.CLOSED) // the client owns the retry
    expect(states.at(-1)).toBe('reconnecting')
    expect(clock.nextDelay()).toBe(3_000)
    clock.advance(3_000)
    const second = FakeEventSource.last()
    expect(second).not.toBe(first)
    expect(second.url).toBe('/api/v1/projects/p1/events/stream?last_event_id=e9')
    second.open()
    expect(states.at(-1)).toBe('live')
    expect(h.onSync).toHaveBeenCalledOnce() // only the first, fresh connection
    expect(h.onRejected).not.toHaveBeenCalled() // it had opened before dropping
  })

  it('backs off exponentially up to 30 s and shows Offline after 3 failed attempts', () => {
    const { client, states, clock } = setup()
    client.start()
    const delays: number[] = []
    const seen: StreamState[] = []
    for (let i = 0; i < 7; i++) {
      FakeEventSource.last().fail()
      delays.push(clock.nextDelay())
      seen.push(states.at(-1)!)
      clock.advance(delays.at(-1)!)
    }
    expect(delays).toEqual([3_000, 6_000, 12_000, 24_000, RETRY_MAX_MS, RETRY_MAX_MS, RETRY_MAX_MS])
    expect(seen.slice(0, 3)).toEqual(['reconnecting', 'reconnecting', 'offline'])
    expect(states.at(-1)).toBe('offline') // still retrying
    // A successful connection resets the backoff.
    FakeEventSource.last().open()
    expect(states.at(-1)).toBe('live')
    FakeEventSource.last().fail()
    expect(clock.nextDelay()).toBe(3_000)
    expect(states.at(-1)).toBe('reconnecting')
  })

  it('counts only consecutive failures toward Offline', () => {
    const { client, states, clock } = setup()
    client.start()
    FakeEventSource.last().fail()
    clock.advance(3_000)
    FakeEventSource.last().fail()
    clock.advance(6_000)
    FakeEventSource.last().open() // resets the count
    FakeEventSource.last().fail()
    expect(states.at(-1)).toBe('reconnecting')
  })

  it('reports refusals before open so the caller can check the session', () => {
    const { client, h } = setup()
    client.start()
    FakeEventSource.last().fail()
    expect(h.onRejected).toHaveBeenCalledOnce()
  })

  it('starts over without a resume point on resync.required, then syncs', () => {
    const { client, h } = setup()
    client.start()
    const first = FakeEventSource.last()
    first.open()
    first.emit('event.created', summary({ id: 'e1' }), 'e1')
    first.emit('resync.required', { reason: 'too_many_missed' })
    expect(first.readyState).toBe(FakeEventSource.CLOSED)
    const second = FakeEventSource.last()
    expect(second.url).toBe('/api/v1/projects/p1/events/stream')
    expect(h.onSync).toHaveBeenCalledTimes(1)
    second.open()
    expect(h.onSync).toHaveBeenCalledTimes(2)
    expect(FakeEventSource.open()).toEqual([second])
  })

  it('stops for good on session.expired', () => {
    const { client, h, clock } = setup()
    client.start()
    const es = FakeEventSource.last()
    es.open()
    es.emit('session.expired', { reason: 'session_ended' })
    expect(h.onSessionExpired).toHaveBeenCalledOnce()
    expect(es.readyState).toBe(FakeEventSource.CLOSED)
    expect(clock.pendingCount()).toBe(0)
    expect(FakeEventSource.instances).toHaveLength(1)
  })

  it('stop closes the connection and cancels a pending retry', () => {
    const { client, clock } = setup()
    client.start()
    FakeEventSource.last().fail()
    expect(clock.pendingCount()).toBe(1)
    client.stop()
    expect(clock.pendingCount()).toBe(0)
    clock.advance(60_000)
    expect(FakeEventSource.instances).toHaveLength(1)
  })

  it('retryNow skips the remaining backoff', () => {
    const { client, clock } = setup()
    client.start()
    FakeEventSource.last().fail()
    client.retryNow()
    expect(FakeEventSource.instances).toHaveLength(2)
    expect(clock.pendingCount()).toBe(0)
  })
})
