# Ticket 10 verification

## Failure cases and agreed boundaries

Before implementation: dialogs disagree about library, artist detail, album
detail and managed playlist invalidation; inactive cached views retain old
names; partial batch responses are ignored and rejected edits look saved;
failed writes must leave existing data truthful and show an error; broad
realtime refresh must retain download, sync, playlist list and stats consumers.
Display cascades and clearing overrides must keep stable IDs and original names.

The ticket specifies the boundaries: real library/detail/playlist query hooks
with rendered rename dialogs and controlled HTTP, plus the production SPA with
intercepted HTTP/WebSocket. Existing dialog tests mock writes and do not read
cached views. The hook regression uses infinite freshness to expose missing
invalidation of inactive views. No new test-only production seam is needed.

## Regression evidence

Baseline: `2a002fc2fd958984ce7d84a79ddf4ae6696953c6` on `main`.
Before production edits, album, artist, track and batch regressions retained
`Before` in the inactive artist-detail cache. The partial batch regression
failed because the dialog silently closed without an alert. The rejected-write
case passed on baseline. All six cases pass after the repair.

## Browser fixture and repeatable artifact

The intercepted production SPA serves two owned tracks from one artist and
album, also present in a managed playlist. HTTP writes maintain display
overrides separately from original records; one batch item and one individual
write are rejected. The scenario warms the list and details, performs individual
track/album/artist edits and batches for all three kinds, verifies partial
failure feedback and accepted data, clears track and album overrides, and
receives `library.updated` over the existing WebSocket. One document request
confirms navigation never discarded the query cache. This verifies frontend
cache/UI behavior; backend cascade and identity behavior remain covered by the
existing backend suites, without changes to their implementation.

From the repository root:

```sh
cd web
npx playwright test e2e/library-rename.spec.ts --trace on --output playwright-report/ticket-10
```

Retained local artifacts (ignored by Git):

```text
web/playwright-report/ticket-10/library-rename-individual--861d4-efresh-warmed-library-views-chromium/trace.zip
web/playwright-report/ticket-10/library-rename-individual--861d4-efresh-warmed-library-views-chromium/library-rename.png
web/playwright-report/ticket-10/library-rename-individual--861d4-efresh-warmed-library-views-chromium/rename-requests.json
```

The trace includes the `rename-requests` attachment (225 requests in the focused
run); the readable JSON copy was extracted alongside it. The screenshot was
visually inspected. Open the trace from `web`:

```sh
npx playwright show-trace playwright-report/ticket-10/library-rename-individual--861d4-efresh-warmed-library-views-chromium/trace.zip
```

## Review

Independent code-review Standards and Spec agents found no actionable findings
in the staged ticket diff. Existing edits in `internal/player/listening.go` and
`internal/player/radio_test.go` are excluded from this change.

## Checks

- Typecheck: passed during implementation and after browser-fixture corrections.
- Focused library/dialog/realtime tests: 22 passed; neighboring artist navigation
  and playlist tests also passed.
- Focused Chromium scenario: passed with retained trace and screenshot.
- `make check`: passed; 130 frontend files / 1,306 tests, Go tests, platform
  vet, generated SQL/transport drift checks and HTTP/event contracts.
- `make check-full`: its `check` stage passed. The race stage failed only at
  `cmd/reverb-testpeer/TestAppFlowAgainstTestPeer` (`smoke_test.go:86`), timing
  out waiting for an offline track to be kept on the phone. Every other race
  package passed. This frontend change does not modify that Go path.
- The failing smoke test passed when rerun alone with the race detector:
  `CGO_CFLAGS='-O2 -g -DHAVE_STRCHRNUL' TMPDIR=/tmp go test -race ./cmd/reverb-testpeer -run '^TestAppFlowAgainstTestPeer$' -count=1`.
- Since Make stops before browsers on a race failure, `cd web && npm run e2e`
  was run separately: all 24 Chromium scenarios passed, including this ticket.
  The original full gate remains recorded as failed; it was not rerun wholesale.
- Lint: zero errors; existing `makeSocket` effect-dependency warning remains.

Full gate command:

```sh
CGO_CFLAGS='-O2 -g -DHAVE_STRCHRNUL' TMPDIR=/tmp make check-full > /tmp/reverb-ticket10-check-full.log 2>&1
```

No native Wails or Xcode compilation is required by this frontend change, and
neither was run.
