import { useQuery, type QueryClient } from '@tanstack/react-query'

import { fetchMe, fetchPublicConfig, isApiError } from '@/lib/api'

export const meQueryKey = ['me'] as const
export const configQueryKey = ['config'] as const

/**
 * The signed-in user, or `null` when there is no valid session. Expiry and
 * verification failures stay errors so callers can show the right state.
 */
export function useMe() {
  return useQuery({
    queryKey: meQueryKey,
    queryFn: async ({ signal }) => {
      try {
        return await fetchMe(signal)
      } catch (err) {
        if (isApiError(err) && err.status === 401 && err.code === 'unauthenticated') return null
        throw err
      }
    },
    retry: false,
    staleTime: 30_000,
    // Re-check when the tab regains focus so a sign-out elsewhere is noticed.
    refetchOnWindowFocus: true,
  })
}

export function usePublicConfig() {
  return useQuery({
    queryKey: configQueryKey,
    queryFn: ({ signal }) => fetchPublicConfig(signal),
    staleTime: 5 * 60_000,
  })
}

/** Drops everything cached for the current user (after sign-in, sign-out, or account changes). */
export async function resetUserData(client: QueryClient): Promise<void> {
  client.removeQueries({ predicate: (q) => q.queryKey[0] !== configQueryKey[0] })
  await client.invalidateQueries({ queryKey: meQueryKey })
}
