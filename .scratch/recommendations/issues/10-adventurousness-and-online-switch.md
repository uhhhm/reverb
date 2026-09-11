# 10: Adventurousness + Online recommendations switch

**What to build:** Two Settings controls. The **Adventurousness** slider shifts every surface's balance of new versus known music around its default (Radio about 50% new, Discover Weekly 100% new, Daily Mixes mostly library). The **Online recommendations** switch, on by default, stops all similarity lookups so recommendations come only from the library.

**Blocked by:** 08

**Status:** ready-for-agent

- [ ] Both settings sync across devices, since they belong to the household's taste profile
- [ ] Adventurousness visibly changes the new-vs-known ratio in Radio, and a test asserts the ratio at the extremes
- [ ] With online recommendations off, no requests reach Last.fm, ListenBrainz, or Deezer similarity endpoints (checked by a test)
- [ ] The switch's description in Settings says what data is sent when it is on
