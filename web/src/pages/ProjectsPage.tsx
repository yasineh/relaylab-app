import { FolderKanban } from 'lucide-react'

import { useMe } from '@/hooks/session'

/**
 * Signed-in landing page. Project management ships in add-project-management;
 * until then this page states plainly that nothing can be created yet.
 */
export function ProjectsPage() {
  const me = useMe()
  return (
    <div className="space-y-6">
      <div className="space-y-1">
        <h1 className="text-2xl font-semibold tracking-tight">Projects</h1>
        <p className="text-muted-foreground">Signed in as {me.data?.name ?? 'you'}.</p>
      </div>
      <section className="flex max-w-xl flex-col items-start gap-3 rounded-lg border border-dashed bg-card p-6">
        <FolderKanban aria-hidden="true" className="size-6 text-primary" />
        <h2 className="font-medium">No projects yet</h2>
        <p className="text-sm text-muted-foreground">
          Creating projects and webhook endpoints is not available in this version of RelayLab yet.
        </p>
      </section>
    </div>
  )
}
