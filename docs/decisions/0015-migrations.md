# D15. Two migration owners, run in order

Status: Accepted

## Context

Better Auth owns its schema and migrations; the app's schema references its users.

## Decision

Better Auth migrations create the `auth` schema (`auth_migrator`), then goose creates the `app` schema (`app_migrator`). App tables reference `auth."user"(id)` with `ON DELETE CASCADE`. Runtime roles are separate and least-privilege; the Go runtime role can only read `auth.session` and `auth.user`.

## Consequences

- Each owner migrates only its schema; the order is enforced locally, in tests, and in deployment.
- A schema-drift test applies both and compares the result with the ERD.

## See also

- [Database ownership and migrations](../architecture/auth.md#database-ownership-and-migrations)
