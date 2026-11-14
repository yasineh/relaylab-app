# D13. React, React Router, TanStack Query, Tailwind and shadcn/ui

Status: Accepted

## Context

The web app needs routing, server-state caching, forms, and an accessible component base.

## Decision

React with React Router (data router, SPA mode), TanStack Query for server state, React Hook Form with Zod, Tailwind CSS with shadcn/ui components, and the official Better Auth React client. The Go API serves the built app.

## Consequences

- One static build embedded in the API binary; no separate web server.

## See also

- [Development guide](../development/README.md)
