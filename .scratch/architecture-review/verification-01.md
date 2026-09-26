# Ticket 01 verification

Baseline: `9fff95bba7acec966503d4f01027450ff2b6afac` on `main`.

## Regression evidence

`TestReloadPreservesDownloadIdentityAndPlaylistChanges` was written and run
before changing production code. It failed on the baseline with both:

- `download after reload has no catalog id`
- `playlist rename after reload is absent from the change log`

It now boots the phone-profile composition root, changes an adapter through
HTTP, completes a download through the yt-dlp process fixture, and verifies its
catalog identity, pending-upload completion hook and playlist change-log entry.

`TestLinkAddFollowsRemovedLibrary` failed before the provider change: `Add` returned
no error after downloads were disabled. It now returns `ErrNoDownloader` without
an HTTP handler first mutating the planner.

`TestTwoDevicesReplicatePlaylistAfterAdapterReload` boots two real runtimes,
pairs them over loopback libp2p, shares a playlist, creates and deletes an adapter,
downloads a track, then verifies the playlist rename and catalog-keyed membership
on the second device. The second device restarts and edits the playlist; the first
device receives the edit. It logs the verified playlist and catalog IDs.

## Repeatable scenario

From the repository root:

```sh
go test ./internal/app -run '^(TestReloadPreservesDownloadIdentityAndPlaylistChanges|TestLinkAddFollowsRemovedLibrary|TestTwoDevicesReplicatePlaylistAfterAdapterReload)$' -count=1 -v | tee /tmp/reverb-ticket01-e2e.log
```

The scenario uses temporary databases and music folders, a tagged MP3-producing
yt-dlp fixture, an external-library HTTP fixture, and real local HTTP/libp2p
transports. It requires host Python and does not invoke real music providers.

## Review

Independent standards and spec reviews found no actionable issues. The review
covered the production diff from the baseline and the new E2E scenarios.

## Checks

- Focused reload regressions and two-device scenario: passed.
- `go test ./internal/wiring ./internal/api ./internal/linkadd`: passed.
- `make check`: passed (Go suite, platform vet, frontend lint/typecheck, 131
  frontend test files / 1,336 tests, generated SQL and transport drift checks,
  and HTTP/event contract tests).
- `make check-full`: passed, including the full Go race suite and all 21
  Chromium browser E2E tests.

The full gate was run as:

```sh
CGO_CFLAGS='-O2 -g -DHAVE_STRCHRNUL' TMPDIR=/tmp make check-full > /tmp/reverb-ticket01-check-full.log 2>&1
```

`TMPDIR` keeps desktop test socket paths short; the C flag supports sqlc's
bundled SQLite build on this macOS host. No native Wails or Xcode build was run;
this ticket changes shared Go composition and the gate includes platform vet.
