# D17. All limits in one configuration module

Status: Accepted

## Context

Limits scattered through the code are hard to find, document, and change.

## Decision

Every limit is read from the central configuration (environment-overridable): projects per user, endpoints per project, live streams, body, header, rate, and retention limits, and the replay, outbound, and demo limits. A test fails if a known limit value is hard-coded elsewhere.

## Consequences

- One place to change a limit; the security doc's limits table is generated from the same code.

## See also

- [Limits](../security.md#limits)
