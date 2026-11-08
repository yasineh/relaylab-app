import type { ReactNode } from 'react'

/** Centered card used by the sign-in, sign-up, and recovery screens. */
export function AuthLayout({
  title,
  description,
  children,
  footer,
}: {
  title: string
  description?: ReactNode
  children: ReactNode
  footer?: ReactNode
}) {
  return (
    <div className="mx-auto w-full max-w-sm py-6">
      <div className="rounded-lg border bg-card p-6 shadow-sm">
        <div className="mb-6 space-y-1.5">
          <h1 className="text-xl font-semibold tracking-tight">{title}</h1>
          {description ? <p className="text-sm text-muted-foreground">{description}</p> : null}
        </div>
        {children}
      </div>
      {footer ? <div className="mt-4 text-center text-sm text-muted-foreground">{footer}</div> : null}
    </div>
  )
}
