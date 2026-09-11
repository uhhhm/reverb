# 03: Radio

**What to build:** "Start Radio" on tracks, artists, albums, and playlists. Radio plays the seed and then an endless stream of recommendations. The player queue refills from the recommendation module before it runs out, and the next external track is resolved in advance so playback doesn't stall. There's no personal ranking yet: candidates come from the available sources in source order.

**Blocked by:** 02

**Status:** ready-for-agent

- [ ] Start Radio is available from the track, artist, album, and playlist menus
- [ ] The queue keeps at least a few tracks ahead. The next external track is resolved before the current one ends
- [ ] The same artist never plays more than twice in a row, and no track repeats within a session
- [ ] Manually queued tracks play before Radio tracks. Clearing the queue or playing something else ends the Radio session
- [ ] Player store tests cover refill, the artist limit, and ending a session
