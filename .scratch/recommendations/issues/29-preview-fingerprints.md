# 29: Preview fingerprints for candidates

**What to build:** Fingerprints for Deezer 30-second previews of recommendation candidates, so an unowned track can be compared to the library by sound.

**Blocked by:** 28

**Status:** needs-info

- [ ] Preview fingerprints are cached keyed by the source track ID; the audio is discarded after embedding and never becomes a library file
- [ ] Cache is bounded and survives a restart
- [ ] A missing or unreachable preview degrades to no fingerprint, never a failed recommendation
- [ ] Fetching previews respects the "Online recommendations" switch
- [ ] Preview embeddings and library embeddings are comparable, despite a 30-second clip against a full track

## Comments

Verify before starting: confirm Deezer is wired as a candidate source and that preview URLs reach the recommendation path. `internal/recommend` shows no Deezer source file, though the spec lists Deezer among candidate sources.
