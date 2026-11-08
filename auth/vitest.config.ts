import { defineConfig } from 'vitest/config'

export default defineConfig({
  test: {
    include: ['test/**/*.test.ts'],
    // Integration tests share one PostgreSQL cluster; each file creates its own database.
    testTimeout: 30_000,
    hookTimeout: 60_000,
  },
})
