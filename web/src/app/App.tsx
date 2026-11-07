import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { useState } from 'react'
import { RouterProvider, createBrowserRouter } from 'react-router'

import { routes } from '@/app/routes'

export function createQueryClient() {
  return new QueryClient({
    defaultOptions: {
      queries: { retry: 1, refetchOnWindowFocus: false, staleTime: 15_000 },
    },
  })
}

type AppProps = {
  /** Injected in tests; defaults to a browser router over the app routes. */
  router?: ReturnType<typeof createBrowserRouter>
  queryClient?: QueryClient
}

export function App({ router, queryClient }: AppProps) {
  const [client] = useState(() => queryClient ?? createQueryClient())
  const [appRouter] = useState(() => router ?? createBrowserRouter(routes))
  return (
    <QueryClientProvider client={client}>
      <RouterProvider router={appRouter} />
    </QueryClientProvider>
  )
}
