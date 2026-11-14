# D12. A built-in test sink as the safe replay destination

Status: Accepted

## Context

Visitors and new users need a replay destination that works without their own server and without any network risk.

## Decision

A replay can target another endpoint of the same project. The worker stores the request there as a new event, marked with its replay, without opening a socket. It is the default destination, and the only one for demo users.

## Consequences

- Replay can be tried safely anywhere, including the public demo.

## See also

- [Replays](../api.md#replays)
