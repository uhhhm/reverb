# 06: ListenBrainz as a source

**What to build:** ListenBrainz similar artists and similar recordings become a third similarity source. Candidates from all sources are merged into one list per request. Each candidate records which sources suggested it, and candidates several sources agree on rank higher until real ranking arrives.

**Blocked by:** 02

**Status:** ready-for-agent

- [ ] ListenBrainz source is registered at the composition root. It needs no user account and respects the service's rate limits
- [ ] MusicBrainz IDs are used for matching when present, with a fallback to artist and title
- [ ] Merged candidates keep the list of sources that suggested them, available to later ranking and reasons
- [ ] One source failing doesn't fail the request. Tests use recorded fixtures
