import { Link } from 'react-router'

import { usePublicConfig } from '@/hooks/session'

/**
 * Site footer. Repository and documentation links appear only when a public
 * repository is configured (PUBLIC_REPO_URL), so no dead link is ever shown.
 */
export function SiteFooter() {
  const config = usePublicConfig()
  const repo = config.data?.repo_url
  return (
    <footer className="border-t bg-card">
      <nav
        aria-label="Footer"
        className="mx-auto flex max-w-6xl flex-wrap items-center gap-x-6 gap-y-2 px-4 py-6 text-sm text-muted-foreground"
      >
        <span>RelayLab: receive, inspect, and replay webhooks.</span>
        <Link to="/security" className="hover:text-foreground hover:underline">
          Security and privacy
        </Link>
        {repo ? (
          <>
            <a href={`${repo}/tree/main/docs`} className="hover:text-foreground hover:underline">
              Documentation
            </a>
            <a href={repo} className="hover:text-foreground hover:underline">
              Source code
            </a>
          </>
        ) : null}
      </nav>
    </footer>
  )
}
