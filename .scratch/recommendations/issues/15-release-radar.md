# 15: Release Radar

**What to build:** Reverb infers Tracked artists from plays and library contents (for example, artists with enough recent plays or library tracks). Every Friday a Release Radar Mix collects their releases from the past week or two, using the Deezer and Spotify release lists.

**Blocked by:** 14

**Status:** ready-for-agent

- [ ] Tracked artists are derived, not stored, and the thresholds are tested
- [ ] New releases are found through the sources' discography capability and deduplicated across sources
- [ ] Release Radar refreshes Friday at local midnight and is empty (and hidden) when there are no new releases
- [ ] Not-interested artists never appear
