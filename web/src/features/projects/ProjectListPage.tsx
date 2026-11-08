import { zodResolver } from '@hookform/resolvers/zod'
import { FolderKanban, Loader2, Plus } from 'lucide-react'
import { useState } from 'react'
import { useForm } from 'react-hook-form'
import { Link } from 'react-router'

import { Alert } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Dialog } from '@/components/ui/dialog'
import { useCreateProject, useProjects, type Project } from '@/features/projects/api'
import { applyProjectError } from '@/features/projects/errors'
import { ProjectFormFields } from '@/features/projects/ProjectFormFields'
import { projectSchema, toInput, type ProjectFormValues } from '@/features/projects/schema'
import { formatDate } from '@/lib/format'

export function ProjectListPage() {
  const projects = useProjects()
  const [creating, setCreating] = useState(false)
  const [focusId, setFocusId] = useState<string | null>(null)

  return (
    <div className="space-y-6">
      <div className="flex flex-wrap items-center justify-between gap-4">
        <div className="space-y-1">
          <h1 className="text-2xl font-semibold tracking-tight">Projects</h1>
          <p className="text-muted-foreground">
            Each project keeps its webhook endpoints and events separate.
          </p>
        </div>
        {projects.data && projects.data.length > 0 ? (
          <Button onClick={() => setCreating(true)}>
            <Plus aria-hidden="true" /> Create project
          </Button>
        ) : null}
      </div>

      {projects.isPending ? (
        <output className="flex items-center gap-2 text-muted-foreground">
          <Loader2 aria-hidden="true" className="size-4 animate-spin" /> Loading projects…
        </output>
      ) : projects.isError ? (
        <Alert tone="error">
          <p>Your projects could not be loaded.</p>
          <Button variant="outline" size="sm" onClick={() => void projects.refetch()}>
            Try again
          </Button>
        </Alert>
      ) : projects.data.length === 0 ? (
        <EmptyState onCreate={() => setCreating(true)} />
      ) : (
        <ProjectGrid projects={projects.data} focusId={focusId} />
      )}

      <CreateProjectDialog
        open={creating}
        onClose={() => setCreating(false)}
        onCreated={(p) => {
          setCreating(false)
          setFocusId(p.id)
        }}
      />
    </div>
  )
}

function EmptyState({ onCreate }: { onCreate: () => void }) {
  return (
    <section className="flex max-w-xl flex-col items-start gap-3 rounded-lg border border-dashed bg-card p-6">
      <FolderKanban aria-hidden="true" className="size-6 text-primary" />
      <h2 className="font-medium">Create your first project</h2>
      <p className="text-sm text-muted-foreground">
        A project groups the webhook endpoints for one integration or environment, such as a payment provider
        sandbox. Only you can see your projects and the events they receive.
      </p>
      <Button onClick={onCreate}>
        <Plus aria-hidden="true" /> Create project
      </Button>
    </section>
  )
}

// Moves focus to a newly created project's card when it renders.
const focusOnMount = (el: HTMLAnchorElement | null) => el?.focus()

function ProjectGrid({ projects, focusId }: { projects: Project[]; focusId: string | null }) {
  return (
    <ul className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3" aria-label="Your projects">
      {projects.map((p) => (
        <li key={p.id}>
          <Link
            ref={p.id === focusId ? focusOnMount : undefined}
            to={`/projects/${p.id}`}
            className="flex h-full flex-col gap-2 rounded-lg border bg-card p-5 transition-colors hover:border-primary/50"
          >
            <span className="font-medium break-words">{p.name}</span>
            {p.description ? (
              <span className="line-clamp-2 text-sm break-words text-muted-foreground">{p.description}</span>
            ) : null}
            <span className="mt-auto text-xs text-muted-foreground">Created {formatDate(p.created_at)}</span>
          </Link>
        </li>
      ))}
    </ul>
  )
}

function CreateProjectDialog({
  open,
  onClose,
  onCreated,
}: {
  open: boolean
  onClose: () => void
  onCreated: (p: Project) => void
}) {
  const create = useCreateProject()
  const [formError, setFormError] = useState<string | null>(null)
  const form = useForm<ProjectFormValues>({
    resolver: zodResolver(projectSchema),
    defaultValues: { name: '', description: '' },
  })
  const { errors, isSubmitting } = form.formState

  function close() {
    form.reset()
    setFormError(null)
    onClose()
  }

  const onSubmit = form.handleSubmit(async (values) => {
    setFormError(null)
    try {
      const project = await create.mutateAsync(toInput(values))
      form.reset()
      onCreated(project)
    } catch (err) {
      setFormError(applyProjectError(err, form.setError))
    }
  })

  return (
    <Dialog open={open} onClose={close} title="Create project" description="You can rename it later.">
      <form noValidate onSubmit={onSubmit} className="space-y-4" aria-label="Create project">
        {formError ? <Alert tone="error">{formError}</Alert> : null}
        <ProjectFormFields register={form.register} errors={errors} autoFocusName />
        <div className="flex justify-end gap-2">
          <Button variant="ghost" onClick={close}>
            Cancel
          </Button>
          <Button type="submit" disabled={isSubmitting}>
            {isSubmitting ? <Loader2 aria-hidden="true" className="animate-spin" /> : null}
            {isSubmitting ? 'Creating…' : 'Create project'}
          </Button>
        </div>
      </form>
    </Dialog>
  )
}
