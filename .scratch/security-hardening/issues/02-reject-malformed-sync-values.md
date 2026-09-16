# 02: A malformed sync change value must not wedge projection recovery

**What to build:** A paired device can no longer stall play-history projection by sending a sync change whose value is not valid JSON. Accepted changes whose values are malformed must be refused at the sync boundary (or safely skipped by recovery) instead of being stored in a form that makes the shared "unprojected plays" recovery query fail. Today a single malformed play record makes recovery error for every later catalog identity, so deferred plays are never projected and the retry loop logs the same failure forever.

**Blocked by:** None (can start immediately)

**Status:** ready-for-agent

- [ ] After a peer sends a play record whose value is not valid JSON, later valid plays that were deferred until their catalog identity arrived are still projected.
- [ ] A malformed value cannot cause the recovery pass for an unrelated catalog identity to fail.
- [ ] Malformed values are refused or quarantined at the sync boundary rather than stored, and existing signature verification and non-emitting peer application are unchanged.
- [ ] A regression test injects a malformed play change through the sync path and asserts projection drains and the pending-projection queue clears.
- [ ] `go test ./internal/sync/... ./internal/materialize/... ./internal/p2p/...` passes.
