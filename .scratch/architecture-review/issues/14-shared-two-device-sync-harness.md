# 14 — One two-device harness for replicated-fact tests

**What to build:** Package tests that replicate a fact between two devices use one shared harness instead of each carrying its own copy. The harness gives each device its own database, change log and materializer, sends one device's log to the other the way a sync round does, and can assert that a change applied on B is not emitted back to A.

**Today there are three copies.** `internal/notinterested/notinterested_test.go:20`, `internal/tastesettings/tastesettings_test.go:19` and `internal/playlistcrdt/playlistcrdt_test.go:19` each define a `device` with `newDevice` and `syncTo`. They differ only in which service they build and hand to the materializer.

This is the harness ticket 08 asks every fact module to be tested through. It is split out because it is useful whatever shape 08's interface takes.

**Blocked by:** None — can start immediately.

**Status:** done

- [x] Before writing the harness, list the ways a replication test can pass while replication is broken. Examples: syncing only one way; the peer re-emitting the applied change, so it echoes back; applying the change on the same store it came from; a materializer missing the service under test. The harness makes each of these fail loudly.
- [x] One harness lives in a test-support package importable from any `internal/...` test (for example `internal/synctest`). It is not compiled into the product binary.
- [x] A device is configured with the services it projects into, so no test repeats store setup, device rows or materializer wiring.
- [x] The harness has a helper asserting that, after a sync A → B, B's log holds no new change authored by B. This is the no-echo check.
- [x] The three existing copies are deleted, and their tests pass unchanged through the shared harness.
- [x] `go test ./internal/notinterested ./internal/tastesettings ./internal/playlistcrdt` passes.

## Comments

Implemented as `internal/sync/pairtest`. Failure modes it turns into test failures, each covered by `pairtest_test.go`:

- syncing a device onto its own store (`SyncTo` refuses the same device, store or log);
- calling a one-way sync convergence (`Converge` syncs both ways and `AssertConverged` compares both logs as sets);
- a peer re-emitting what it applied (`SyncToWithoutEcho` fails on any new change authored by the receiver);
- a materializer missing the service under test (`SyncToWithoutEcho` fails when the receiver's projected tables did not change, so the no-echo check cannot pass vacuously);
- a peer the receiver does not know (`SyncTo` fails when a rejected change's author is not a known device).
