# 04: Not interested

**What to build:** The owner can mark a track or an artist Not interested from track menus and artist pages. The mark is a replicated fact, so it syncs to every paired device. Marked items never appear in any recommendation surface. A Settings list shows marks and lets the owner undo them.

**Blocked by:** 01

**Status:** ready-for-agent

- [ ] Marking works for library tracks, search-source tracks, and artists. Identity follows the existing catalog-ID vs backend-ID rules, so the same recording marked on two devices is one mark
- [ ] Marks replicate through the sync change log and apply on peers without re-emitting
- [ ] Recommendation results exclude marked tracks and every track by a marked artist
- [ ] Undo removes the mark on every device
- [ ] Sync tests cover replication and concurrent mark and undo
