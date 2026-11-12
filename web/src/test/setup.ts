import '@testing-library/jest-dom/vitest'
import { cleanup } from '@testing-library/react'
import { cleanStores } from 'nanostores'
import { afterAll, afterEach, beforeEach, vi } from 'vitest'

import { authClient } from '@/lib/auth-client'
import { FakeEventSource } from '@/test/event-source'

beforeEach(() => {
  // Live streams use a fake EventSource that tests drive explicitly.
  FakeEventSource.instances = []
  vi.stubGlobal('EventSource', FakeEventSource)
  // Tests never touch the network: any request a test did not mock fails loudly.
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
    throw new Error(`Unmocked fetch in test: ${input instanceof Request ? input.url : String(input)}`)
  })
})

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})

afterAll(() => {
  // Better Auth's session store (nanostores) tears down one second after its last
  // listener leaves, touching `window`. Run that teardown now, while the test
  // environment still exists, instead of after Vitest has destroyed it.
  const { session } = (authClient as unknown as { $store: { atoms: Record<string, unknown> } }).$store.atoms
  cleanStores(session as Parameters<typeof cleanStores>[0])
})
