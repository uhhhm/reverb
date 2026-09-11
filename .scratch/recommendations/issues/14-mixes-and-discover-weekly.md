# 14: Mixes + Discover Weekly

**What to build:** The Mix concept and its first instance. A Mix is a generated, regularly refreshed list of recommendations. It is not a playlist and doesn't sync as one (ADR 0002). Discover Weekly refreshes Monday at local midnight with about 30 tracks that are all new to the library. It is shown on Home and has its own page with play, shuffle, and "Save as playlist".

**Blocked by:** 04, 05, 08

**Status:** ready-for-agent

- [ ] Mix generation is deterministic for a given period seed and set of inputs, so two devices with the same synced data produce the same Discover Weekly (ADR 0001)
- [ ] A refresh replaces the previous Mix. No history is kept
- [ ] Save as playlist creates an ordinary managed playlist, including search-source tracks, which can then join an offline set
- [ ] A device that was asleep at refresh time regenerates on next launch
- [ ] At most N tracks per artist in one Mix
