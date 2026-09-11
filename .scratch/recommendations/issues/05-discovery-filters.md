# 05: Discovery filters

**What to build:** Recommendations stop showing junk and repeats. Duplicate versions of one recording (remasters, the same track on several albums) collapse to one. Live, cover, remix, and karaoke versions are excluded unless the seed is one. Discovery surfaces exclude tracks already in the library or played recently. Each surface declares whether it is a discovery surface.

**Blocked by:** 03

**Status:** done

- [x] Duplicate versions collapse using fingerprinting plus normalised titles, with test cases for remaster and deluxe suffixes
- [x] Version-type detection handles common title patterns ("Live", "(Remix)", "Cover", "Karaoke", "Instrumental"), with table tests
- [x] Discovery surfaces exclude owned and recently played tracks (the recency window is configurable in code)
- [x] Radio keeps owned tracks (it isn't discovery-only) but still applies the version and duplicate rules

## Comments

No discovery surface exists yet (Radio and Similar tracks both keep owned tracks), so the discovery rule is covered by unit tests on `filterTracks` and wired through `WithRecentPlays`; For you shelves and Mixes will declare `surfacePolicy{discovery: true}`.
