# 17: Last.fm history as taste input

**What to build:** When a Last.fm account is linked for scrobbling, its listening history (top artists and tracks, and recent scrobbles from before Reverb) feeds the taste profile. This gives a new install a rich profile immediately.

**Blocked by:** 08

**Status:** ready-for-agent

- [ ] Imported history is taste input only. It never creates plays and never replicates as plays
- [ ] Scrobbles Reverb itself sent aren't double-counted
- [ ] Unlinking Last.fm removes its contribution to the profile
- [ ] The quality test with Last.fm history is no worse than without
