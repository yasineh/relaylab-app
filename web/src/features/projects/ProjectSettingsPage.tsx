import { zodResolver } from '@hookform/resolvers/zod'
import { Loader2 } from 'lucide-react'
import { useId, useState } from 'react'
import { useForm } from 'react-hook-form'
import { useNavigate } from 'react-router'

import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Alert } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { useDeleteProject, useUpdateProject, type Project } from '@/features/projects/api'
import { applyProjectError } from '@/features/projects/errors'
import { useCurrentProject } from '@/features/projects/ProjectLayout'
import { ProjectFormFields } from '@/features/projects/ProjectFormFields'
import { projectSchema, toInput, type ProjectFormValues } from '@/features/projects/schema'

export function ProjectSettingsPage() {
  const project = useCurrentProject()
  return (
    <div className="max-w-2xl space-y-8">
      <DetailsSection project={project} />
      <DeleteSection project={project} />
    </div>
  )
}

function DetailsSection({ project }: { project: Project }) {
  const update = useUpdateProject(project.id)
  const [status, setStatus] = useState<{ tone: 'success' | 'error'; message: string } | null>(null)
  const form = useForm<ProjectFormValues>({
    resolver: zodResolver(projectSchema),
    values: { name: project.name, description: project.description ?? '' },
  })
  const { errors, isSubmitting } = form.formState
  const onSubmit = form.handleSubmit(async (values) => {
    setStatus(null)
    try {
      await update.mutateAsync(toInput(values))
      setStatus({ tone: 'success', message: 'Project details saved.' })
    } catch (err) {
      const message = applyProjectError(err, form.setError)
      if (message) setStatus({ tone: 'error', message })
    }
  })
  return (
    <section aria-labelledby="details-heading" className="rounded-lg border bg-card p-6">
      <h2 id="details-heading" className="mb-4 font-semibold">
        Project details
      </h2>
      <form noValidate onSubmit={onSubmit} className="space-y-4" aria-label="Project details">
        {status ? <Alert tone={status.tone}>{status.message}</Alert> : null}
        <ProjectFormFields register={form.register} errors={errors} />
        <Button type="submit" disabled={isSubmitting}>
          {isSubmitting ? <Loader2 aria-hidden="true" className="animate-spin" /> : null}
          {isSubmitting ? 'Saving…' : 'Save changes'}
        </Button>
      </form>
    </section>
  )
}

function DeleteSection({ project }: { project: Project }) {
  const del = useDeleteProject(project.id)
  const navigate = useNavigate()
  const inputId = useId()
  const [typed, setTyped] = useState('')
  const [error, setError] = useState<string | null>(null)
  const matches = typed === project.name

  async function onDelete(e: React.FormEvent) {
    e.preventDefault()
    if (!matches) return
    setError(null)
    try {
      await del.mutateAsync()
      void navigate('/projects', { replace: true })
    } catch {
      setError('The project could not be deleted. Try again.')
    }
  }

  return (
    <section aria-labelledby="delete-heading" className="rounded-lg border border-destructive/40 bg-card p-6">
      <h2 id="delete-heading" className="font-semibold">
        Delete project
      </h2>
      <p className="mt-1 mb-4 text-sm text-muted-foreground">
        Permanently deletes this project and everything in it, including its endpoints and events. This cannot
        be undone.
      </p>
      <form onSubmit={(e) => void onDelete(e)} className="space-y-4" aria-label="Delete project">
        {error ? <Alert tone="error">{error}</Alert> : null}
        <div className="space-y-1.5">
          <Label htmlFor={inputId}>
            Type <span className="font-mono break-all">{project.name}</span> to confirm
          </Label>
          <Input id={inputId} autoComplete="off" value={typed} onChange={(e) => setTyped(e.target.value)} />
        </div>
        <Button type="submit" variant="destructive" disabled={!matches || del.isPending}>
          {del.isPending ? <Loader2 aria-hidden="true" className="animate-spin" /> : null}
          {del.isPending ? 'Deleting…' : 'Delete this project'}
        </Button>
      </form>
    </section>
  )
}
