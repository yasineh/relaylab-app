import type { RouteObject } from 'react-router'

import { AppShell } from '@/app/AppShell'
import { AccountSettingsPage } from '@/features/auth/AccountSettingsPage'
import { CheckEmailPage } from '@/features/auth/CheckEmailPage'
import { ForgotPasswordPage } from '@/features/auth/ForgotPasswordPage'
import { RequireAuth } from '@/features/auth/RequireAuth'
import { ResetPasswordPage } from '@/features/auth/ResetPasswordPage'
import { SignInPage } from '@/features/auth/SignInPage'
import { SignUpPage } from '@/features/auth/SignUpPage'
import { VerifyEmailPage } from '@/features/auth/VerifyEmailPage'
import { ProjectLayout } from '@/features/projects/ProjectLayout'
import { ProjectListPage } from '@/features/projects/ProjectListPage'
import { ProjectOverviewPage } from '@/features/projects/ProjectOverviewPage'
import { ProjectSettingsPage } from '@/features/projects/ProjectSettingsPage'
import { HomePage } from '@/pages/HomePage'
import { NotFoundPage } from '@/pages/NotFoundPage'

export const routes: RouteObject[] = [
  {
    path: '/',
    element: <AppShell />,
    children: [
      { index: true, element: <HomePage /> },
      // Public authentication screens.
      { path: 'sign-in', element: <SignInPage /> },
      { path: 'sign-up', element: <SignUpPage /> },
      { path: 'check-email', element: <CheckEmailPage /> },
      { path: 'verify-email', element: <VerifyEmailPage /> },
      { path: 'forgot-password', element: <ForgotPasswordPage /> },
      { path: 'reset-password', element: <ResetPasswordPage /> },
      // Dashboard: requires an authenticated, verified user (checked by the Go API).
      {
        element: <RequireAuth />,
        children: [
          { path: 'projects', element: <ProjectListPage /> },
          {
            path: 'projects/:projectId',
            element: <ProjectLayout />,
            children: [
              { index: true, element: <ProjectOverviewPage /> },
              { path: 'settings', element: <ProjectSettingsPage /> },
            ],
          },
          { path: 'settings/account', element: <AccountSettingsPage /> },
        ],
      },
      { path: '*', element: <NotFoundPage /> },
    ],
  },
]
