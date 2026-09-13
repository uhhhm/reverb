# 08: Taste profile ranking + reasons

**What to build:** Candidates are ranked by the household's taste profile instead of source order. The profile is derived locally from synced inputs:
- plays: skips (short plays), completions, repeats, and recency
- tracks added to the library or to playlists
- not-interested marks

Ranking features include source agreement, closeness to your taste, whether the artist is familiar, popularity, and novelty. Every recommendation shows a short reason ("Because you played X", "Fans of Y also like").

**Blocked by:** 07

**Status:** done

- [x] Profile is built only from replicated inputs, so devices with the same data rank identically (ADR 0001)
- [x] Ranking is a small, inspectable model (e.g. weighted features or logistic regression trained on your own history), not an opaque service
- [x] The quality test shows an improvement over the ticket 07 baseline, and the new baseline is saved
- [x] Reasons show in Radio, the similar tracks list, and wherever recommendations appear
- [x] Profile rebuilds incrementally and stays fast on a library of about 20k plays

## Comments

- Model: weighted features in `internal/recommend/rank.go`; profile in `taste.go`. Radio NDCG@10 on the fixture rose from 0.944 to 1.0; similar tracks is unchanged (its one miss is not among the candidates).
- Only qualified plays are stored (half the track or four minutes), so the skip signal is a play that was not completed. Recording shorter skips would need a new replicated input.
- Tracks added to the library are not an input: nothing replicated records a library addition. Playlist tracks are.
- No source reports popularity; a source's rank stands in for it.
