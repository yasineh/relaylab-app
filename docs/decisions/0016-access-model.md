# D16. No open sign-up; visitors use demo workspaces

Status: Accepted

## Context

A public deployment must not hand out unlimited accounts, but visitors should be able to try the product.

## Decision

`AUTH_SIGNUP_ENABLED` is false in production (and needs an explicit acknowledgement to turn on). Visitors start demo workspaces: Better Auth anonymous users with seeded synthetic data, tighter limits, sink-only replay, a banner, deleted after 24 hours. Real accounts are created with an operator command.

## Consequences

- The demo is self-service and isolated per visitor.
- Implementation: the auth service, which owns the `auth` schema, deletes expired demo users; their app data goes by cascade.

## See also

- [Demo users](../architecture/auth.md#demo-users)
