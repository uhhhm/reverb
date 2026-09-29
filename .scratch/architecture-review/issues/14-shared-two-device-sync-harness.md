# 14 — One two-device harness for replicated-fact tests

**What to build:** Package tests that replicate a fact between two devices use one shared harness instead of each carrying its own copy. The harness gives each device its own database, change log and materializer, sends one device's log to the other the way a sync round does, and can assert that a change applied on B is not emitted back to A.

**Today there are three copies.** `internal/notinterested/notinterested_test.go:20`, `internal/tastesettings/tastesettings_test.go:19` and `internal/playlistcrdt/playlistcrdt_test.go:19` each define a `device` with `newDevice` and `syncTo`. They differ only in which service they build and hand to the materializer.

This is the harness ticket 08 asks every fact module to be tested through. It is split out because it is useful whatever shape 08's interface takes.

**Blocked by:** None — can start immediately.

**Status:** ready-for-agent

- [ ] Before writing the harness, list the ways a replication test can pass while replication is broken. Examples: syncing only one way; the peer re-emitting the applied change, so it echoes back; applying the change on the same store it came from; a materializer missing the service under test. The harness makes each of these fail loudly.
- [ ] One harness lives in a test-support package importable from any `internal/...` test (for example `internal/synctest`). It is not compiled into the product binary.
- [ ] A device is configured with the services it projects into, so no test repeats store setup, device rows or materializer wiring.
- [ ] The harness has a helper asserting that, after a sync A → B, B's log holds no new change authored by B. This is the no-echo check.
- [ ] The three existing copies are deleted, and their tests pass unchanged through the shared harness.
- [ ] `go test ./internal/notinterested ./internal/tastesettings ./internal/playlistcrdt` passes.
