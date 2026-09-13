# 10: Adventurousness + Online recommendations switch

**What to build:** Two Settings controls. The **Adventurousness** slider shifts every surface's balance of new versus known music around its default (Radio about 50% new, Discover Weekly 100% new, Daily Mixes mostly library). The **Online recommendations** switch, on by default, stops all similarity lookups so recommendations come only from the library.

**Blocked by:** 08

**Status:** done

- [x] Both settings sync across devices, since they belong to the household's taste profile
- [x] Adventurousness visibly changes the new-vs-known ratio in Radio, and a test asserts the ratio at the extremes
- [x] With online recommendations off, no requests reach Last.fm, ListenBrainz, or Deezer similarity endpoints (checked by a test)
- [x] The switch's description in Settings says what data is sent when it is on

## Comments

- With the switch off, Radio and the similar sections come back unavailable; library-only recommendations arrive with ticket 11. Discover Weekly and Daily Mixes do not exist yet, so only Radio has a new-music share so far.
- Settings made before a device first pairs are not backfilled to the peer (the same holds for Not interested marks).
