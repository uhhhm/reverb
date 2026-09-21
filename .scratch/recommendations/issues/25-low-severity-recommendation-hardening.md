# 25: Low-severity recommendation hardening batch

**What to build:** Four independent, low-severity robustness fixes surfaced by review of the recommendations work:

1. Bound the ListenBrainz upload client so a stalled endpoint cannot block the scrobble worker indefinitely. Done ticket 22 bounded only the account-free read source; the upload adapter added alongside it still has no timeout.
2. Keep the artist merge's MBID index in sync. When a merge learns an MBID for an existing artist candidate, record it in the MBID index so a later same-MBID candidate with a different name dedups instead of producing a duplicate artist card (the track merge already does this).
3. Guard the Mix route against unknown kinds so it does not fetch a fallback Mix or set the wrong document title before rendering "Mix not found".
4. Bound Last.fm response-body reads so a hostile or errant endpoint cannot buffer an unbounded body.

**Blocked by:** none

**Status:** done

Evidence (verified at `b18b9a4`):

- `internal/scrobble/listenbrainz/adapter.go:32` — `New()` uses `http.DefaultClient` (no timeout); `Service.RunWorker` submits via `drainOnce` synchronously with the long-lived app context (`internal/scrobble/service.go:316`, `internal/app/build.go:642`). Contrast `internal/recommend/listenbrainz/source.go:57`.
- `internal/recommend/artists.go:313-315` — sets `out[idx].MBID` without updating `byMBID`; `internal/recommend/tracks.go:254-258` performs that update.
- `web/src/routes/Mix.tsx:36-37,62` — `useDocumentTitle` and `useMix` run before the `known` guard; an unknown kind falls back to `discoverWeekly`.
- `internal/scrobble/lastfm/adapter.go:314` — the new `getJSON` helper reads the whole body with `io.ReadAll` and no limit.

Acceptance criteria:

- [x] ListenBrainz upload requests carry a timeout independent of `http.DefaultClient`; a stalled `httptest` server test asserts the call returns a bounded error rather than hanging.
- [x] An artist candidate that gains an MBID during merge is indexed by MBID, so a later same-MBID candidate with a different name merges rather than duplicating. Regression test.
- [x] An unknown `/mix/:kind` issues no recommendations request and sets no wrong title; it renders the not-found state. Existing Mix tests still pass.
- [x] Last.fm JSON response reads are size-limited with no behavioural change for normal responses.
- [x] Focused Go tests for the touched packages and the focused web tests pass.
