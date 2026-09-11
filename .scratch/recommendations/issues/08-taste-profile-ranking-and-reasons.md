# 08: Taste profile ranking + reasons

**What to build:** Candidates are ranked by the household's taste profile instead of source order. The profile is derived locally from synced inputs:
- plays: skips (short plays), completions, repeats, and recency
- tracks added to the library or to playlists
- not-interested marks

Ranking features include source agreement, closeness to your taste, whether the artist is familiar, popularity, and novelty. Every recommendation shows a short reason ("Because you played X", "Fans of Y also like").

**Blocked by:** 07

**Status:** ready-for-agent

- [ ] Profile is built only from replicated inputs, so devices with the same data rank identically (ADR 0001)
- [ ] Ranking is a small, inspectable model (e.g. weighted features or logistic regression trained on your own history), not an opaque service
- [ ] The quality test shows an improvement over the ticket 07 baseline, and the new baseline is saved
- [ ] Reasons show in Radio, the similar tracks list, and wherever recommendations appear
- [ ] Profile rebuilds incrementally and stays fast on a library of about 20k plays
