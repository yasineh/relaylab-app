import { Radio } from 'lucide-react'
import { Link, Outlet } from 'react-router'

/**
 * Application frame. Navigation only lists sections that exist; later changes
 * add Projects, Endpoints, Events, and Settings as they ship (no dead links).
 */
export function AppShell() {
  return (
    <div className="flex min-h-screen flex-col">
      <a
        href="#main"
        className="sr-only focus:not-sr-only focus:absolute focus:left-4 focus:top-4 focus:rounded-md focus:bg-background focus:px-3 focus:py-2"
      >
        Skip to content
      </a>
      <header className="border-b">
        <div className="mx-auto flex h-14 max-w-6xl items-center px-4">
          <Link to="/" className="flex items-center gap-2 font-semibold tracking-tight">
            <Radio aria-hidden="true" className="size-5 text-primary" />
            RelayLab
          </Link>
        </div>
      </header>
      <main id="main" className="mx-auto w-full max-w-6xl flex-1 px-4 py-10">
        <Outlet />
      </main>
    </div>
  )
}
