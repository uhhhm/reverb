# Ticket 11 verification

Agreed seams from ticket 11: Manager Enqueue, Retry, Cancel, Clear,
ClearFinished, Status and Start/Stop against real SQLite persistence; Device
HTTP Downloads and pending uploads across a restart. Controlled downloaders
and completion dependencies exercise failures, without new production test seams.

Failure inventory (before implementation):
- Output exists but pending phase/output persistence fails: retain in memory,
  block cancellation, retry writes; do not claim restart durability.
- Required bookkeeping fails: persist pending phase and output before the hook;
  retry/repeated Add/restart reuse bytes and asynchronous reference.
- Display error changes: never changes lifecycle decisions.
- Hook succeeds but terminal row write fails: no completion event or linking;
  retry with idempotent pending-upload and recommendation attribution records.
- Concurrent retries/recovery: one terminal transition/publication.
- Legacy prefix rows migrate once; other running/failed rows remain unchanged.
- Restart between each durable step: the persisted pending row owns recovery.

Primary regression owner: real-store Manager lifecycle tests. Existing memory
store tests cannot detect atomic SQL update failures or error-independent restart.
Device scenario separately covers production hook wiring and transport decoding.

## Implementation and regression evidence

- `completion_pending` is internal persisted state, excluded from job JSON.
  Migration 0053 converts only legacy running completion-prefix rows.
- One completion owner retains output before recording and only publishes and
  scans after an atomic terminal update. Request-read failures keep it pending.
- Recommendation additions use a stable Download job identity and reuse the
  persisted attribution and timestamp on retries.
- Cancellation rereads under the transition lock. Asynchronous polling owns that
  lock through confirmation and output handoff, preventing cancellation/clearing
  from removing its recovery row. A slow external poll can delay other completion
  transitions (Lidarr's HTTP timeout is 30 seconds per request).

The initial real-store regression failed because changing display text allowed
Cancel to discard output. The Device scenario then failed with two attribution
records after repeated terminal-write failures. Review regressions also failed
before repair for request-read failure, cancellation overwriting an established
pending phase, and cancellation/clearing while a completed Poll was returning.
Each now passes at its owning boundary.

Fixtures are in `internal/download/completion_e2e_test.go`,
`internal/download/completion_persistence_test.go`, and
`internal/app/download_completion_e2e_test.go`. SQLite triggers fault real job
updates and pending-upload inserts. The application fixture creates synthetic ID3
MP3 bytes through the controlled yt-dlp process and counts actual acquisitions.
The legacy fixture restores the version-52 schema, upgrades, closes/reopens twice,
and checks preserved output, request, attribution and asynchronous reference.
No credentials or private music are used.

## Exact rerun commands

```sh
CGO_CFLAGS='-O2 -g -DHAVE_STRCHRNUL' go test ./internal/download -run 'Completion|CancellationOverlapping' -count=1 -v
REVERB_COMPLETION_REPORT="$PWD/.scratch/architecture-review/verification-11.json" CGO_CFLAGS='-O2 -g -DHAVE_STRCHRNUL' go test ./internal/app -run '^TestDeviceDownloadCompletionSurvivesRecordingAndTerminalWriteFailures$' -count=1 -v
CGO_CFLAGS='-O2 -g -DHAVE_STRCHRNUL' TMPDIR=/tmp make check-full
cd web && npm run e2e
```

The JSON report is emitted after the Device scenario verifies one acquisition,
one pending-upload row, one recommendation-add row, retained bytes and a linked,
completed job after restart. The internal Manager scenarios additionally cover
synchronous and asynchronous pending/final persistence failures, overlapping Add
and Retry, request hydration failure, both cancellation orderings, singular
completion publication and idempotent legacy upgrades.

## Final validation

- Focused download, persistence, recommendation attribution, API and application
  packages passed. The final owner suite passed under the race detector.
- The Device scenario passed on the final implementation and regenerated
  `verification-11.json` from its observed assertions.
- `make check-full` passed with `CGO_CFLAGS='-O2 -g -DHAVE_STRCHRNUL' TMPDIR=/tmp`.
  This includes `make check`, platform vet checks, backend/desktop/mobile tests,
  typechecking, lint, 130 frontend test files / 1,306 tests, SQL/contract drift
  checks, HTTP/event contract tests, the full Go race suite and 24 Chromium E2E
  tests. Lint reported the existing `realtimeWiring.ts` hook-dependency warning;
  no lint errors. Earlier broad runs were stopped for review repairs; the final
  run completed successfully.
- Native Wails/Xcode compilation was not run; no native source or wire schema
  changed. HTTP, WebSocket, frontend and generated iOS contracts remain compatible.
- The pre-existing edits to `internal/player/listening.go` and
  `internal/player/radio_test.go` were left outside the ticket commit.

## Standards

No remaining documented-standard breaches or actionable code-smell findings.
The review's request-read failure finding was reproduced and repaired. Tests use
existing Manager/Device interfaces and real persistence without new test-only
production seams.

## Spec

No remaining functional gaps or unrequested scope. Review reproduced and repaired
request-read failure, stale asynchronous cancellation after output handoff, and
cancellation/clearing during a returning completion Poll. Restart, output
retention, idempotent records, migration and publication ordering are covered.

Final findings: Standards 0; Spec 0. No unresolved issue on either axis.
