# Ticket 12 verification

Seams: `download.Manager` (Enqueue, Start/Stop, Cancel, Retry, Clear,
ClearFinished, reconcile) against real SQLite, and the device HTTP API over
`app.Build` for both downloader lanes.

## Transitions and competing actions (enumerated before implementation)

Legal transitions: queued→running (worker start or async submission);
running→running+completion pending (output exists, ticket 11); pending→completed;
running→failed (chain exhausted, timeout, async failure or age, restart of a sync
job); queued/running→canceled; running→queued (shutdown); failed/canceled→queued
(Retry, a new attempt); completed/failed/canceled→removed (Clear).

Competing actions and the rule each now follows:

| Race | Before | Rule |
|---|---|---|
| Completion vs cancel | ticket 11 | Once output exists, completion owns it; cancel and clear are refused. Cancel before output ends the attempt canceled. |
| Worker read the job, then a cancel landed | worker overwrote canceled with running and downloaded | The start is a transition on the stored row; a registered worker is stopped instead of racing it. |
| Queued job canceled and retried: two dispatches | the stale dispatch re-ran the finished job | Only a queued row starts; stale dispatches are dropped. |
| Async submission returns after cancel | job resurrected as running | Submission applies only to the queued attempt that placed it; otherwise the external request is abandoned (`CancelAsync`). |
| Async submission returns after clear | external request orphaned | Same; the row is never recreated. |
| Late progress sample | could overwrite a later transition | Conditional write for the running attempt only. |
| Automatic retry timer vs manual retry | stale timer drove the newer attempt | The timer is bound to the attempt that failed. |
| Automatic retry after Stop/reload | stopped manager queued the job into a dead channel | A stopped manager does nothing. |
| Terminal write refused by the store | failure published anyway | Published only after persisting. |
| Slow external poll | held the global lock, blocking every job | Polled outside the lock; only a Cancel of that job waits. |
| Retry vs Clear | Clear could delete a job just retried | Delete removes only finished rows; an update never recreates a removed row. |
| Retry vs restart | `attempts`, downloader, ref and `started_at` were not persisted by SQLite, so attempts stayed 0, the auto-retry cap never applied, and a retried async job was timed out by its first attempt | The job row persists the whole lifecycle; the manual URL is persisted before the job is queued. |

Regressions (all failed before the change): `internal/download/lifecycle_test.go`.
Barriers: a store that returns a stale read and holds, a held async Submit, the
paused queue, and a one-worker sentinel job.

## Implementation

`internal/download/lifecycle.go` holds the transition owner: re-read under
`transitionMu`, decide against the stored row and attempt, stamp timestamps,
persist, then publish. `process`, `submitAsync`, `reconcileJob`, recovery,
`Cancel`, `Retry` and automatic retry use it. Completion stays with ticket 11's
`completeOutput`, now attempt-checked. A new attempt starts at the top of its
fallback chain, as it did in production before (SQLite never kept the fallback).
An async poll runs outside the transition lock and is applied as a transition
afterwards, so a slow Lidarr holds up only its own job: a Cancel of that job
waits for the poll (keeping ticket 11's guarantee that a confirmed output is
handed to completion first). Clear and ClearFinished need no lock; the
conditional delete keeps them safe.

Code review follow-ups (each with a regression): a stale submission no longer
abandons a retry that Lidarr handed the same album ref; a Retry that lands
while the canceled attempt's worker is still unwinding is redispatched when
that worker finishes; Retry re-checks the job before persisting its request.

## Application scenarios and artifact

`internal/app/download_lifecycle_e2e_test.go`:
- Sync lane (phone, controlled yt-dlp stub with file holds): cancel before
  output then retry; cancel and clear refused after output reached completion;
  retry with a manual URL while paused, restart, recovery runs it with the URL;
  ClearFinished removes exactly the finished jobs while one is running.
- Async lane (desktop, fake Lidarr whose album search is held): a queued album
  row is submitted at boot, canceled and cleared while the search is held, then
  released; a running album is canceled, Lidarr reports it imported, the device
  restarts.

Each checks the database row, the job list, published events and the list after
restart. Reports: `download-lifecycle-sync.json`, `download-lifecycle-async.json`.
Removing the late-submission abandonment or the attempts column write fails the
scenarios.

## Exact rerun commands

```sh
CGO_CFLAGS='-O2 -g -DHAVE_STRCHRNUL' go test ./internal/download/... -count=1
CGO_CFLAGS='-O2 -g -DHAVE_STRCHRNUL' go test -race ./internal/download/... -count=1
REVERB_E2E_ARTIFACTS="$PWD/.scratch/architecture-review" CGO_CFLAGS='-O2 -g -DHAVE_STRCHRNUL' go test ./internal/app -run 'TestDownloadControlsFollowOneLifecycle' -count=1 -v
```
