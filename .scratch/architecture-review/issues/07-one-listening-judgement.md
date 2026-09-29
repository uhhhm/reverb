# 07: One listening judgement for play recording and Radio steering

**What to build:** A player session's playback samples are judged once, and that verdict drives both what is recorded as a play and how Radio steers. Every reader of listening history sees plays through one source that has already applied the qualified rule.

**Today "what was listened to" is decided twice, then re-filtered by hand on every read.**

- **Two counters over the same samples.**
  - `internal/player/listening.go` accumulates heard time and decides "qualified": longer than 30 s, and heard for half or 4 minutes (`:136-138`).
  - Radio keeps its own counter in `internal/player/radio.go` (`:120-186`), with its own literals: a 5000 ms step at `:139`, 1500 ms from the end at `:143`, and less than half heard counted as a skip at `:165`.
- **The two rules disagree.** A 10-minute track heard for 4.5 minutes is a qualified listen but a Radio skip. A 20-second track can complete for Radio but never qualifies.
  - `docs/architecture.md` documents both rules, so which one should steer Radio is an owner decision, not a typo.
- **Stats uses a third definition.** A recommendation skip there is `qualified = 0` among recommendation-origin plays (`plays.sql:211`).
- **Every reader re-applies the filter by hand.** `p.qualified = 1` is repeated in 17 of the 33 queries in `internal/store/queries/plays.sql` and in `internal/store/db/taste.go:22`. Nothing enforces it; ticket 05 fixes the readers that forgot it.
- **Radio's seam leaks into api.**
  - `player.RadioFetch` is adapted in `internal/api/player.go:225-254`.
  - That adapter converts `player.RadioSeed` to `recommend.Seed` structurally, so the two must keep identical fields.
  - It builds queue tracks as a hand-written `map[string]any` with `"recommendationOrigin": "radio"`, while `listening.go:94` also uses the literal `"radio"`.
  - Seed validation (1–5 seeds) is done twice: `player/radio.go:88` and `recommend/radio.go:12`.

**The deepening:**
- One listening judge per entry, living in `internal/player`, produces a verdict: qualified, skipped or completed. Play recording and Radio steering both consume it.
- Reads of listening history go through one qualified-listens source, a SQL view or a single shared CTE.
- The Radio fetch adapter moves out of `internal/api` into the core, typed on `recommend.Seed` and `core.RecommendationRadio`.

This continues ADR 0003 (queue and Radio policy live in the core).

**Decision (see Comments).** A skip means leaving a play without qualifying or completing it. *Completed* means reaching the end having heard at least half. Radio steering, play recording and Stats all read this one verdict.

**Blocked by:** 05

**Status:** done

- [x] The owner decides the Radio skip rule, recorded in a comment here.
- [x] `docs/architecture.md` states the single rule (qualified, completed, skipped), replacing both the `internal/player` listening rule and the Radio "less than half" skip wording.
- [x] Write tests first, at the `player.Service` interface. They cover each boundary case (30 s floor, half, 4-minute cap, seeks, repeat from the start, leaving early), and each asserts both the recorded play and the Radio steering direction.
- [x] `internal/player/radio.go` keeps no heard-time counter of its own.
- [x] Stats' recommendation skip rate counts plays that are neither qualified nor completed (`plays.sql` `RecommendationStats`), so a recommendation track under 30 s that plays to the end is not a skip.
- [x] No hand-written `qualified = 1` remains in `plays.sql` or `store/db`. Reads use the shared qualified-listens source, and a test shows an unqualified play is excluded from Summary, Top, Timeline, Clock and Entity stats.
- [x] `internal/api` contains no Radio seed conversion or queue-track construction.
- [x] `go test ./internal/player ./internal/play ./internal/recommend/... ./internal/api ./internal/app` and `make gen-check` pass. The listening and offline-Radio e2e tests in `internal/app` still pass.

## Comments

**Decision: skip = neither qualified nor completed (2026-09-29).** One judge per entry produces `qualified` and `completed`:
- *qualified* uses the existing rule: longer than 30 s, and heard for half or 4 minutes.
- *completed* means the play reached its end having heard at least half, so seeking to the end is not a completion.
- *skipped* means leaving the play without qualifying or completing it.

Radio steering, play recording and Stats all consume this one verdict.

Neither option in the ticket works alone, because each gets one end of the duration range wrong:

| Case | listening.go | Radio (under half) | Stats (`qualified = 0`) | Chosen rule |
|---|---|---|---|---|
| 10-minute track, 4.5 minutes heard, then left | qualified | skip | not a skip | not a skip (qualified) |
| 20-second track played to the end | never qualifies | completed | skip | not a skip (completed) |
| 3-minute track, 40 s heard, then left | not qualified | skip | skip | skip |

- "Skip = not qualified" would call a finished 20-second interlude a skip.
- "Under half" would call 4.5 minutes of listening a skip, which the 4-minute cap was chosen to count as a listen.

**Compatibility.** Plays already replicate `completed`, so Stats moves to `qualified = 0 AND completed = 0` with no schema or wire change.

The `completed` flag already stored on recommendation plays is judged by position only. An older play that was seeked to the end therefore counts as completed in Stats. We accept that. New plays record `completed` from the verdict.

**Blocked by 05:** 05 is done (`4c8a7f4`).

