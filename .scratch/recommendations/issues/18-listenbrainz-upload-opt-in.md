# 18: ListenBrainz upload (opt-in)

**What to build:** A Settings option to connect a ListenBrainz account and upload listens. This unlocks ListenBrainz's collaborative-filtering recommendations for the user, which then feed in as another candidate source. It's off by default, and the setting explains that ListenBrainz listens are public.

**Blocked by:** 06

**Status:** ready-for-agent

- [ ] The ListenBrainz token is stored like other secrets and is never logged
- [ ] Listens are submitted through the existing scrobble queue, so they retry offline
- [ ] Personal ListenBrainz recommendations appear as a candidate source when connected
- [ ] Disconnecting stops uploads and removes the source
