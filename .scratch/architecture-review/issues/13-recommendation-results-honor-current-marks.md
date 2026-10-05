# 13 — Every recommendation result honors current Not interested marks

**What to build:** Every recommendation result returned to a caller honors the household's current Not interested marks, whether the candidates were just fetched, read from a warm cache or retained as an offline fallback. Undo restores eligibility from the retained candidates. If current marks cannot be read, the surface is unavailable and returns no recommendations.

The filtering helpers already exist, but individual return paths must remember to use them. Give the recommendation module a shared final exclusion policy that all result paths cross. Retain early filtering needed to choose seeds, rank candidates and fill a surface; a final filter does not replace those distinct responsibilities. This is independent of ticket 09, which makes the browser ask for an updated result.

**Blocked by:** None — can start immediately.

**Status:** done

- [x] Before implementation, enumerate fresh, cached, stale/offline and refresh-fallback paths and write failing regressions for any path that bypasses current exclusions. Exercise the recommendation module through its caller-facing interface.
- [x] Similar artists, similar tracks, Radio, Home shelves, individual/listed Mixes and playlist suggestions apply the shared final exclusion policy on every returned result, including early returns and retained results after failed refreshes. There is no separate exclusion implementation in HTTP handlers.
- [x] Current marks are read once per request and used consistently through that result. An explicitly unconfigured exclusion source remains distinct from a configured source whose read failed; a failed read never returns unfiltered cached recommendations.
- [x] Marking a track or artist removes it from subsequent results backed by a cache populated before the mark. Undo can return it from that retained candidate set without requiring the external source to be reachable or regenerating a Mix solely to undo presentation filtering.
- [x] Final filtering does not mutate cached candidate slices or stored Mixes/shelves. Preserve result timestamps, offline/refreshing metadata, ranking and ordering, seed exclusions, version rules, discovery rules, personal-source eligibility and surface limits.
- [x] Preserve the distinction between historical inputs used to generate a period's Mix and current exclusions applied when reading it. Keep early filtering where it affects seed selection or ranking; consolidation must not reintroduce rejected seeds or manufacture replacements outside the existing generation policy.
- [x] Returned refresh/fallback results honor exclusions as well as ordinary reads. Background work must not silently persist an empty replacement because the marks store temporarily failed.
- [x] An application-level HTTP scenario warms track and artist results, shelves, Mixes and playlist suggestions; marks a track and an artist; makes external sources unavailable; and then reads the cached/offline surfaces and requests Radio. Undo a mark, inject an exclusion-store read failure, and recover it. Verify visible response contents and that prior successful cached data survives the failure.
- [x] Keep a machine-readable surface-by-surface result report, captured responses and exact rerun command as the repeatable verification artifact. Separate current exclusions from historical generation expectations in the fixture.
- [x] Focused recommendation and application checks, `make check` and `make check-full` pass. Preserve existing transport contracts, or regenerate and check them if a necessary contract change is made; record any unavailable or failing check accurately.
