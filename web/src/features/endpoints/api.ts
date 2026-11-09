import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import { apiFetch } from '@/lib/api'

/** An endpoint as returned by /api/v1/projects/{projectId}/endpoints (endpoint-management spec). */
export type Endpoint = {
  id: string
  project_id: string
  name: string
  enabled: boolean
  /** The receiving URL. Null only when the stored token cannot be decrypted; rotating issues a new one. */
  url: string | null
  last_received_at: string | null
  created_at: string
  updated_at: string
}

export type EndpointPatch = { name?: string; enabled?: boolean }

export const ENDPOINT_NAME_MAX = 100

export const endpointsKey = (projectId: string) => ['endpoints', projectId] as const
export const endpointKey = (projectId: string, id: string) => ['endpoint', projectId, id] as const

const path = (projectId: string, id?: string, suffix = '') =>
  `/api/v1/projects/${encodeURIComponent(projectId)}/endpoints` +
  (id ? `/${encodeURIComponent(id)}` : '') +
  suffix

export function useEndpoints(projectId: string) {
  return useQuery({
    queryKey: endpointsKey(projectId),
    queryFn: async ({ signal }) => (await apiFetch<{ items: Endpoint[] }>(path(projectId), { signal })).items,
  })
}

export function useEndpoint(projectId: string, id: string) {
  return useQuery({
    queryKey: endpointKey(projectId, id),
    queryFn: ({ signal }) => apiFetch<Endpoint>(path(projectId, id), { signal }),
  })
}

/** Stores a changed endpoint in its detail cache and the project's list cache. */
function useStoreEndpoint(projectId: string) {
  const client = useQueryClient()
  return (e: Endpoint) => {
    client.setQueryData(endpointKey(projectId, e.id), e)
    client.setQueryData<Endpoint[]>(endpointsKey(projectId), (list) =>
      list?.map((x) => (x.id === e.id ? e : x)),
    )
    void client.invalidateQueries({ queryKey: endpointsKey(projectId) })
  }
}

export function useCreateEndpoint(projectId: string) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: (name: string) =>
      apiFetch<Endpoint>(path(projectId), { method: 'POST', body: JSON.stringify({ name }) }),
    onSuccess: (e) => {
      client.setQueryData<Endpoint[]>(endpointsKey(projectId), (list) => (list ? [e, ...list] : [e]))
      client.setQueryData(endpointKey(projectId, e.id), e)
      void client.invalidateQueries({ queryKey: endpointsKey(projectId) })
    },
  })
}

export function useUpdateEndpoint(projectId: string, id: string) {
  const store = useStoreEndpoint(projectId)
  return useMutation({
    mutationFn: (patch: EndpointPatch) =>
      apiFetch<Endpoint>(path(projectId, id), { method: 'PATCH', body: JSON.stringify(patch) }),
    onSuccess: store,
  })
}

export function useRotateEndpoint(projectId: string, id: string) {
  const store = useStoreEndpoint(projectId)
  return useMutation({
    mutationFn: () => apiFetch<Endpoint>(path(projectId, id, '/rotate'), { method: 'POST' }),
    onSuccess: store,
  })
}

export function useDeleteEndpoint(projectId: string, id: string) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: () => apiFetch<void>(path(projectId, id), { method: 'DELETE' }),
    onSuccess: () => {
      client.setQueryData<Endpoint[]>(endpointsKey(projectId), (list) => list?.filter((e) => e.id !== id))
      client.removeQueries({ queryKey: endpointKey(projectId, id) })
      void client.invalidateQueries({ queryKey: endpointsKey(projectId) })
    },
  })
}

/** The ready-to-run curl example from the spec, with the URL single-quoted for the shell. */
export const curlExample = (url: string) =>
  `curl -X POST '${url}' -H 'Content-Type: application/json' -d '{"hello":"relaylab"}'`
