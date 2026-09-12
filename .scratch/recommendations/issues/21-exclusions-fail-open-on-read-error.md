# 21: Not-interested exclusions fail open on a read error

**What to build:** A failure to read not-interested marks must never cause marked tracks or artists to be recommended. Today `Service.excluded` swallows the store error and returns `nil`, and every caller treats `nil` as "nothing is marked", so a transient DB error silently re-admits content the owner explicitly banned.

**Blocked by:** none

**Status:** ready-for-agent

Evidence (verified at `5cd26ea`):

- `internal/recommend/recommend.go:140-150` — `excluded` logs and returns `nil` on error.
- `internal/recommend/recommend.go:153-176` — `withoutMarkedArtists` / `withoutMarkedTracks` keep a candidate when `ex == nil`.
- The `ex == nil` case is only legitimate when the exclusion loader is not configured (`s.exclusions == nil`), which is a different condition from a read error.

Acceptance criteria:

- A store error while loading marks must not cause marked content to be returned. Propagate the error so the caller can fail the surface, or otherwise block the result; do not conflate it with "exclusions not configured".
- Add a regression test that injects an erroring exclusion loader and asserts marked tracks/artists are not returned (and that the request surfaces an error rather than an empty-but-successful result).
- Keep the unconfigured-loader case (feature disabled) filter-free.

- [ ] Error path distinguished from unconfigured loader
- [ ] Regression test for failing exclusion loader
- [ ] `go test ./internal/recommend/...` passes
