# 05: Skipped attempts stop counting as co-occurrence

**What to build:** Offline Radio and local similarity rank tracks by what the owner actually listened to together, not by what Radio queued and the owner skipped.

A recommendation is recorded as a play even when it is skipped: it is stored with `qualified = 0` so skip rates stay measurable (`internal/player/listening.go`). Every stats and taste read filters those out with `p.qualified = 1`, but three readers don't:
- **`LocalRecommendationTracks`.** Its session co-occurrence term joins `plays ps` to `plays pc` on `session_id` with no qualified filter (`internal/store/queries/plays.sql:258-262`).
- **`LocalRecommendationArtists`,** the same term (`plays.sql:278-287`).
- **The hand-written playable-library scoring** used on a phone (`internal/store/db/local_playable.go`, about line 86).

A Radio session that plays five tracks the owner skips therefore makes all five co-occur, and each one is scored up (weight 4) as similar to the others. That feeds back into the next offline Radio.

**Also:** `cmd/recommend-quality` evaluates against unfiltered history. `ListAllPlays` is read with no filter (`internal/recommend/quality/files.go:99`), and every play is marked `Completed: true` (`quality.go:307`). So the offline evaluator scores against skips that production excludes.

**Blocked by:** None

**Status:** done

- [x] Write a failing test first, against a real SQLite store. Two tracks that co-occur only through unqualified plays in one session score zero co-occurrence. The same two tracks, qualified, still score.
- [x] The same holds for the artist query and the phone's playable-library scoring.
- [x] The quality tool's evaluation history contains only qualified plays, and the checked-in baseline is regenerated with the reason noted in the commit.
- [x] `go test ./internal/store/... ./internal/recommend/...` passes, and `make gen-check` passes after the sqlc change.

**Note on the baseline.** Regenerating `internal/recommend/quality/testdata/baseline.json` produces no change. The checked-in fixture (`history.json`) records no qualified flag, so it has no skipped plays to drop. The filter only affects `-db` runs against a real database.
