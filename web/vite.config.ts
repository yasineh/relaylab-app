/// <reference types="vitest/config" />
import path from 'node:path'
import tailwindcss from '@tailwindcss/vite'
import react from '@vitejs/plugin-react'
import { defineConfig, loadEnv } from 'vite'

// In development the Vite server proxies API, ingestion and health routes to the
// Go API so the browser stays same-origin (D2). In production the Go binary
// serves the built app itself.
//
// Settings come from the repository-root .env (shared with the backend). Only
// non-secret dev settings are read here; nothing is exposed to client code.
export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, path.resolve(import.meta.dirname, '..'), '')
  const setting = (name: string, fallback: string) => process.env[name] ?? env[name] ?? fallback

  // Inside Docker Compose the container always listens on 5173 (WEB_PORT);
  // natively it uses WEB_HOST_PORT so APP_ORIGIN matches.
  const port = Number(process.env.WEB_PORT ?? setting('WEB_HOST_PORT', '5173'))
  const apiTarget = setting('API_PROXY_TARGET', 'http://localhost:8080')

  return {
    plugins: [react(), tailwindcss()],
    resolve: {
      alias: { '@': path.resolve(import.meta.dirname, './src') },
    },
    server: {
      host: setting('VITE_HOST', 'localhost'),
      port,
      strictPort: true,
      proxy: {
        '/api': { target: apiTarget, changeOrigin: false },
        '/h': { target: apiTarget, changeOrigin: false },
        '/healthz': { target: apiTarget },
        '/readyz': { target: apiTarget },
      },
    },
    build: {
      outDir: 'dist',
      emptyOutDir: true,
      sourcemap: false,
    },
    test: {
      environment: 'jsdom',
      globals: true,
      setupFiles: ['./src/test/setup.ts'],
      css: false,
    },
  }
})
