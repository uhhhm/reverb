# 23: Recommendation attempts only scrobble genuine listens

**What to build:** A play that came from a recommendation surface but does not qualify as a listen (a short skip) must still be recorded for outcome stats, but must never be uploaded to Last.fm or ListenBrainz. Two paths currently leak skips outward:

1. `POST /plays` enqueues a scrobble for every recorded play, ignoring the play's `qualified` flag.
2. The web play tracker declares a recommendation complete whenever playback is within 1.5s of the end without first requiring a known, positive duration, so a recommended track of unknown length is submitted as completed the instant it loads.

Both must be fixed so that only genuine listens reach external services, while unqualified attempts remain measurable in Stats.

**Blocked by:** none

**Status:** done

Evidence (verified at `b18b9a4`):

- `internal/api/plays.go:45-61` — `handlePlay` enqueues unconditionally; it never reads `in.Qualified`.
- `internal/play/service.go` — `Record` already persists `qualified = 0` when `Qualified` is explicitly false, so the signal exists but the scrobble path ignores it.
- `web/src/lib/playTracker.ts:68-84` — `submitRecommendation` sends `qualified: qualify(state)` on every recommendation attempt, including 0–10s skips.
- `web/src/lib/playTracker.ts:151` — the recommendation completion check lacks the `track.durationMs > 0` guard that the ordinary path has at line 156.
- `internal/api/scrobble_test.go:491` (`TestPlays_LinkedUserEnqueuesScrobble`) covers only the qualified path; there is no unqualified regression test.
- `docs/architecture.md` — "short recommendation skips are retained for quality stats but excluded from taste and ordinary listening stats"; an external scrobble is a claimed listen and must be excluded too.

Acceptance criteria:

- [x] A play stored by `POST /plays` enqueues no scrobble when `qualified` is explicitly false, for both Last.fm and ListenBrainz links.
- [x] A qualified play still enqueues exactly one scrobble per active link; a play that omits `qualified` (older clients, ordinary playback) keeps the historical default and still scrobbles.
- [x] A recommendation with an unknown or zero duration is not submitted as completed on load; completion is only declared once `durationMs > 0` and playback reaches the end, matching ordinary plays.
- [x] API regression test: posting an unqualified play for a linked user inserts zero scrobble-queue rows; the existing qualified test still passes.
- [x] Web regression test: a recommendation whose `durationMs` is 0 is not recorded as completed immediately.
- [x] `go test ./internal/api/... ./internal/play/...` and the focused `playTracker` vitest suite pass.
