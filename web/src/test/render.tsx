import { render } from '@testing-library/react'
import type { QueryClient } from '@tanstack/react-query'
import { createMemoryRouter } from 'react-router'

import { App, createQueryClient, sessionLostHandler } from '@/app/App'
import { routes } from '@/app/routes'

/** Renders the real app (routes, guard, global 401 handling) at `path`. */
export function renderApp(path: string) {
  const router = createMemoryRouter(routes, { initialEntries: [path] })
  const holder: { client?: QueryClient } = {}
  const client = createQueryClient(
    sessionLostHandler(
      () => router,
      () => holder.client!,
    ),
  )
  holder.client = client
  // No retries in tests: failures should surface immediately.
  client.setDefaultOptions({ queries: { ...client.getDefaultOptions().queries, retry: false } })
  const utils = render(<App router={router} queryClient={client} />)
  return { ...utils, router, client }
}

export const currentPath = (router: ReturnType<typeof createMemoryRouter>) =>
  router.state.location.pathname + router.state.location.search
