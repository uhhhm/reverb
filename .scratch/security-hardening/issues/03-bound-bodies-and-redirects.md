# 03: Bound recommendation response bodies and restrict updater download redirects

**What to build:** Two independent, low-risk hardening changes from the security review. First, the ListenBrainz recommendation source reads outbound responses through a size cap consistent with its sibling Last.fm and ListenBrainz clients, so a compromised upstream cannot drive unbounded allocation. Second, the desktop updater only follows HTTP redirects to the expected GitHub hosts over HTTPS, so a redirect cannot move the download off the trusted origin.

**Blocked by:** None (can start immediately)

**Status:** done

- [x] An oversized or unbounded response from the recommendation source is bounded and surfaces an error instead of growing memory without limit, with a test covering it.
- [x] The updater refuses a redirect to a host or scheme outside the allowed set and does not write the artifact; a test covers it.
- [x] Normal successful recommendation fetches and normal desktop downloads/updates are unchanged.
- [x] `go test ./internal/recommend/... ./desktop/...` passes.
