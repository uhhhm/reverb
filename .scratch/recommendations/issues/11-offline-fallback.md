# 11: Offline fallback

**What to build:** With no internet (or with online recommendations off), Radio and the similar sections still work using only library tracks. Similarity comes from local signals: artists that appear in the same playlists, tracks played in the same sessions, and shared album or artist. Surfaces that need online data show their last cached results with an offline note.

**Blocked by:** 03

**Status:** ready-for-agent

- [ ] Radio started offline plays only locally available tracks and never stalls trying to resolve an external track
- [ ] A local similarity source is used automatically whenever online sources are unavailable
- [ ] Cached shelves show when they were last updated and an offline note
- [ ] Tests simulate network failure and assert library-only results
