# Ticket 13 verification

Seam: the recommendation module's exported results (`SimilarArtists`,
`SimilarTracks`/`SimilarTracksFor`, `Radio`, `PlaylistSuggestions`, `Shelves`,
`RefreshShelves`, `Mix`, `RefreshMix`), and the device's HTTP API over
`app.Build`.

Path inventory (before implementation):
- Fresh: online lookup for similar tracks/artists, Radio, suggestions, a
  regenerated Mix or shelves.
- Cached: warm in-memory similarity entries; stored shelves and Mixes.
- Stale/offline: expired similarity entry while online is off or lookups fail;
  local-library fallback.
- Refresh fallback: `RefreshShelves` and `RefreshMix` returning the stored
  result when regeneration produced nothing.

Bypasses found by failing regressions (`internal/recommend/final_exclusions_test.go`):
- `RefreshShelves` returned the stored shelves unfiltered when a refresh found nothing.
- `RefreshMix` returned a regenerated Discover Weekly holding a currently marked
  personal-source track, and the stored Mix unfiltered when regeneration failed.
- `RefreshMix` with unreadable marks returned the stored Mix's tracks and rewrote
  it as offline.

Implementation: `serve` in `internal/recommend/recommend.go` is the final
exclusion policy every exported result crosses. It reads marks once per request,
returns the unavailable result without running the producer when they cannot be
read, and filters into new slices so cached candidates and stored Mixes/shelves
keep what undo restores. Early filtering for seed choice, ranking and filling a
surface stays where it was. Handlers carry no exclusion logic.

Application scenario (`internal/app/recommendation_exclusions_e2e_test.go`):
the desktop boots with Deezer, a Last.fm key and plays from nine days earlier.
A routing `http.DefaultTransport` stands in for Deezer, Last.fm and ListenBrainz
(503). Phases: warm, mark an artist and a track, internet down, online
recommendations off (stale similar-artists entry), undo the artist mark,
`not_interested` renamed away (read failure), and recovery. Every surface's
visible entries per phase are in `recommendation-exclusions.json`; the raw HTTP
bodies are in `recommendation-exclusions-responses.json`. The fixture separates
historical generation inputs (plays, no marks at generation) from current
exclusions. Disabling the final filter fails both the unit regressions and the
scenario.

Out of scope, noted: Discover Weekly generation also filters candidates by the
marks current at generation time (through `similarTracks`), so a device
generating mid-week after a mark stores a different Mix than one that generated
on Monday. That is a generation-policy question, not a read-time bypass.

## Exact rerun commands

```sh
go test ./internal/recommend -count=1
REVERB_E2E_ARTIFACTS="$PWD/.scratch/architecture-review" go test ./internal/app -run '^TestRecommendationSurfacesHonorCurrentMarks$' -count=1 -v
```
