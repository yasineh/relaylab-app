import '@testing-library/jest-dom/vitest'
import { cleanup } from '@testing-library/react'
import { afterEach, beforeEach, vi } from 'vitest'

beforeEach(() => {
  // Tests never touch the network: any request a test did not mock fails loudly.
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
    throw new Error(`Unmocked fetch in test: ${input instanceof Request ? input.url : String(input)}`)
  })
})

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
})
