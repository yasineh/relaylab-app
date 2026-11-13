import { useQueryClient } from '@tanstack/react-query'
import { LogOut, UserRound } from 'lucide-react'
import { Link, NavLink, Outlet, useNavigate } from 'react-router'

import logoIcon from '@/assets/brand/logo-icon.svg'
import { Button, buttonVariants } from '@/components/ui/button'
import { DemoBanner } from '@/features/demo/DemoBanner'
import { useMe, usePublicConfig } from '@/hooks/session'
import { authClient } from '@/lib/auth-client'
import { cn } from '@/lib/utils'

/**
 * Application frame. Navigation only lists sections that exist; later changes
 * add Endpoints, Events, and Settings as they ship (no dead links).
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
      <header className="border-b bg-card">
        <div className="mx-auto flex h-14 max-w-6xl items-center gap-6 px-4">
          <Link to="/" className="flex items-center gap-2 font-bold tracking-tight">
            <img src={logoIcon} alt="" aria-hidden="true" className="size-7" />
            <span>
              Relay<span className="text-primary">Lab</span>
            </span>
          </Link>
          <AccountNav />
        </div>
      </header>
      <DemoBanner />
      <main id="main" className="mx-auto w-full max-w-6xl flex-1 px-4 py-10">
        <Outlet />
      </main>
    </div>
  )
}

const navLink = ({ isActive }: { isActive: boolean }) =>
  cn(
    'rounded-md px-2 py-1 text-sm font-medium transition-colors hover:text-foreground',
    isActive ? 'text-foreground' : 'text-muted-foreground',
  )

function AccountNav() {
  const me = useMe()
  const config = usePublicConfig()
  const queryClient = useQueryClient()
  const navigate = useNavigate()

  async function signOut() {
    await authClient.signOut()
    queryClient.clear()
    void navigate('/sign-in?reason=signed-out', { replace: true })
  }

  if (me.isPending) return <div className="ml-auto" />

  if (!me.data) {
    return (
      <nav aria-label="Account" className="ml-auto flex items-center gap-2">
        <Link to="/sign-in" className={buttonVariants({ variant: 'ghost', size: 'sm' })}>
          Sign in
        </Link>
        {config.data?.signup_enabled ? (
          <Link to="/sign-up" className={buttonVariants({ size: 'sm' })}>
            Create account
          </Link>
        ) : null}
      </nav>
    )
  }

  return (
    <>
      <nav aria-label="Main" className="flex items-center gap-2">
        <NavLink to="/projects" className={navLink}>
          Projects
        </NavLink>
      </nav>
      <nav aria-label="Account" className="ml-auto flex items-center gap-1">
        <NavLink
          to="/settings/account"
          className={({ isActive }) =>
            cn(
              buttonVariants({ variant: 'ghost', size: 'sm' }),
              isActive && 'bg-accent text-accent-foreground',
            )
          }
        >
          <UserRound aria-hidden="true" />
          <span className="max-w-40 truncate">{me.data.name}</span>
        </NavLink>
        <Button variant="ghost" size="sm" onClick={() => void signOut()}>
          <LogOut aria-hidden="true" />
          Sign out
        </Button>
      </nav>
    </>
  )
}
