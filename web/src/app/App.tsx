import { MutationCache, QueryCache, QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { useState } from 'react'
import { RouterProvider, createBrowserRouter } from 'react-router'

import { routes } from '@/app/routes'
import { isApiError } from '@/lib/api'
import { signInPath } from '@/lib/redirect'

type AppRouter = ReturnType<typeof createBrowserRouter>

/** Routes where a 401 is expected and must not trigger the "session expired" redirect. */
const PUBLIC_AUTH_PATHS = [
  '/sign-in',
  '/sign-up',
  '/check-email',
  '/verify-email',
  '/forgot-password',
  '/reset-password',
]

/**
 * True for a 401 that means "the session you were using is no longer valid".
 * A plain `unauthenticated` from GET /api/v1/me is not an error: useMe turns it
 * into `null` and the route guard redirects without a notice.
 */
export function isSessionLost(err: unknown): boolean {
  return isApiError(err) && err.status === 401
}

export function createQueryClient(onSessionLost?: () => void) {
  const handle = (err: unknown) => {
    if (onSessionLost && isSessionLost(err)) onSessionLost()
  }
  return new QueryClient({
    queryCache: new QueryCache({ onError: handle }),
    mutationCache: new MutationCache({ onError: handle }),
    defaultOptions: {
      queries: {
        retry: (count, err) => !isApiError(err) && count < 1,
        refetchOnWindowFocus: false,
        staleTime: 15_000,
      },
    },
  })
}

/** Sends the user to sign-in with a "session expired" notice and drops their cached data. */
export function sessionLostHandler(getRouter: () => AppRouter, getClient: () => QueryClient) {
  return () => {
    const router = getRouter()
    const { pathname, search, hash } = router.state.location
    if (PUBLIC_AUTH_PATHS.includes(pathname)) return
    getClient().clear()
    void router.navigate(signInPath(pathname + search + hash, 'expired'), { replace: true })
  }
}

type AppProps = {
  /** Injected in tests; defaults to a browser router over the app routes. */
  router?: AppRouter
  queryClient?: QueryClient
}

export function App({ router, queryClient }: AppProps) {
  const [appRouter] = useState(() => router ?? createBrowserRouter(routes))
  const [client] = useState(() => {
    const holder: { client?: QueryClient } = {}
    const created =
      queryClient ??
      createQueryClient(
        sessionLostHandler(
          () => appRouter,
          () => holder.client!,
        ),
      )
    holder.client = created
    return created
  })
  return (
    <QueryClientProvider client={client}>
      <RouterProvider router={appRouter} />
    </QueryClientProvider>
  )
}
