# 02: Similar tracks, matched to playable results

**What to build:** From a track's menu or page, the owner can see "Similar tracks" from Last.fm similar tracks. Each candidate (artist and title only) is matched to something playable: the library track if owned, otherwise a search-source result that plays through external playback. The Last.fm key already used for scrobbling is reused. Last.fm is registered as a similarity source at the composition root.

**Blocked by:** 01

**Status:** ready-for-agent

- [ ] Similar tracks list shows matched results, and each plays when clicked
- [ ] Owned candidates resolve to the library track by catalog ID. Everything else resolves to a search result, and candidates that can't be matched are dropped
- [ ] Matching reuses the existing matching and normalisation logic, with tests covering wrong-version and wrong-artist cases
- [ ] Matched results are cached. Rate limits are respected, with backoff on errors
- [ ] Hidden gracefully when Last.fm isn't configured
