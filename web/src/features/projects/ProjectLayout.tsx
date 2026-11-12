import { FolderX, Loader2 } from 'lucide-react'
import { Link, NavLink, Outlet, useOutletContext, useParams } from 'react-router'

import { Alert } from '@/components/ui/alert'
import { Button, buttonVariants } from '@/components/ui/button'
import { useProject, type Project } from '@/features/projects/api'
import { isApiError } from '@/lib/api'
import { cn } from '@/lib/utils'

/**
 * Loads the project for /projects/:projectId/* and renders its sub-navigation.
 * Only sections that exist are listed: Replays is added by the change that
 * ships it.
 */
export function ProjectLayout() {
  const { projectId = '' } = useParams()
  const project = useProject(projectId)

  if (project.isPending) {
    return (
      <output className="flex items-center gap-2 text-muted-foreground">
        <Loader2 aria-hidden="true" className="size-4 animate-spin" /> Loading project…
      </output>
    )
  }
  if (project.isError) {
    if (isApiError(project.error) && project.error.status === 404) return <ProjectNotFound />
    if (isApiError(project.error) && project.error.status === 401) return null // global handler redirects
    return (
      <Alert tone="error">
        <p>This project could not be loaded.</p>
        <Button variant="outline" size="sm" onClick={() => void project.refetch()}>
          Try again
        </Button>
      </Alert>
    )
  }

  const p = project.data
  const tab = ({ isActive }: { isActive: boolean }) =>
    cn(
      '-mb-px border-b-2 px-1 pb-2 text-sm font-medium transition-colors',
      isActive
        ? 'border-primary text-foreground'
        : 'border-transparent text-muted-foreground hover:text-foreground',
    )
  return (
    <div className="space-y-6">
      <div className="space-y-1">
        <p className="text-sm text-muted-foreground">
          <Link to="/projects" className="underline-offset-4 hover:underline">
            Projects
          </Link>
        </p>
        <h1 className="text-2xl font-semibold tracking-tight break-words">{p.name}</h1>
      </div>
      <nav aria-label="Project" className="flex gap-5 border-b">
        <NavLink end to={`/projects/${p.id}`} className={tab}>
          Overview
        </NavLink>
        <NavLink to={`/projects/${p.id}/endpoints`} className={tab}>
          Endpoints
        </NavLink>
        <NavLink to={`/projects/${p.id}/events`} className={tab}>
          Events
        </NavLink>
        <NavLink to={`/projects/${p.id}/settings`} className={tab}>
          Settings
        </NavLink>
      </nav>
      <Outlet context={p satisfies Project} />
    </div>
  )
}

/** The project loaded by ProjectLayout. */
export const useCurrentProject = () => useOutletContext<Project>()

export function ProjectNotFound() {
  return (
    <section className="mx-auto flex max-w-md flex-col items-center gap-3 py-16 text-center">
      <FolderX aria-hidden="true" className="size-8 text-muted-foreground" />
      <h1 className="text-xl font-semibold">Project not found</h1>
      <p className="text-sm text-muted-foreground">It may have been deleted, or the link is wrong.</p>
      <Link to="/projects" className={buttonVariants({ variant: 'outline' })}>
        Back to projects
      </Link>
    </section>
  )
}
