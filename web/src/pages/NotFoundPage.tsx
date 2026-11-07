import { Link } from 'react-router'

import { buttonVariants } from '@/components/ui/button'

export function NotFoundPage() {
  return (
    <div className="space-y-4">
      <h1 className="text-2xl font-semibold">Page not found</h1>
      <p className="text-muted-foreground">The page you requested does not exist.</p>
      <Link to="/" className={buttonVariants({ variant: 'outline' })}>
        Back to home
      </Link>
    </div>
  )
}
