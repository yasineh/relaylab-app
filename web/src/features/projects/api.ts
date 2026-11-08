import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import { apiFetch } from '@/lib/api'

/** A project as returned by /api/v1/projects (project-management spec). */
export type Project = {
  id: string
  name: string
  description: string | null
  created_at: string
  updated_at: string
}

export type ProjectInput = { name: string; description: string | null }

export const NAME_MAX = 80
export const DESCRIPTION_MAX = 500

export const projectsKey = ['projects'] as const
export const projectKey = (id: string) => ['project', id] as const

const path = (id?: string) => (id ? `/api/v1/projects/${encodeURIComponent(id)}` : '/api/v1/projects')

export function useProjects() {
  return useQuery({
    queryKey: projectsKey,
    queryFn: async ({ signal }) => (await apiFetch<{ items: Project[] }>(path(), { signal })).items,
  })
}

export function useProject(id: string) {
  return useQuery({
    queryKey: projectKey(id),
    queryFn: ({ signal }) => apiFetch<Project>(path(id), { signal }),
  })
}

export function useCreateProject() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: (input: ProjectInput) =>
      apiFetch<Project>(path(), { method: 'POST', body: JSON.stringify(input) }),
    onSuccess: (project) => {
      client.setQueryData<Project[]>(projectsKey, (list) => (list ? [project, ...list] : [project]))
      client.setQueryData(projectKey(project.id), project)
      void client.invalidateQueries({ queryKey: projectsKey })
    },
  })
}

export function useUpdateProject(id: string) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: (input: Partial<ProjectInput>) =>
      apiFetch<Project>(path(id), { method: 'PATCH', body: JSON.stringify(input) }),
    onSuccess: (project) => {
      client.setQueryData(projectKey(id), project)
      void client.invalidateQueries({ queryKey: projectsKey })
    },
  })
}

export function useDeleteProject(id: string) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: () => apiFetch<void>(path(id), { method: 'DELETE' }),
    onSuccess: () => {
      client.setQueryData<Project[]>(projectsKey, (list) => list?.filter((p) => p.id !== id))
      client.removeQueries({ queryKey: projectKey(id) })
      void client.invalidateQueries({ queryKey: projectsKey })
    },
  })
}
