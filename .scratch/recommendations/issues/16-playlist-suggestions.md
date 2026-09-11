# 16: Playlist suggestions

**What to build:** A managed playlist shows "Suggested songs" below its tracks, seeded from the playlist's contents and ranked by taste. The owner can add a suggestion in one click (Add to playlist, no download) or refresh the list.

**Blocked by:** 08

**Status:** ready-for-agent

- [ ] Suggestions exclude tracks already in the playlist
- [ ] Adding a suggestion replicates like any playlist edit, and it drops off the suggestion list
- [ ] Refresh replaces the list with the next best candidates
- [ ] Hidden for mirrored (synced-mode) playlists, which are rebuilt from upstream
