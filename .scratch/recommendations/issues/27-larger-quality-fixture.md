# 27: Larger quality fixture

**What to build:** The current fixture is too small to measure anything. `internal/recommend/quality/testdata/baseline.json` records 10 training plays and 4 hidden plays, and Radio already scores hit rate, recall@10 and NDCG@10 of exactly 1.0. No ranking change can beat a saturated baseline, so ticket 19's quality criterion is unmeasurable as things stand.

Grow the anonymised fixture until both surfaces have headroom, and save the new baseline.

**Blocked by:** none

**Status:** ready-for-human

- [ ] Fixture is large enough that neither surface scores 1.0 on the ranking metrics
- [ ] Still anonymised, still no personal data beyond anonymised IDs (ticket 07)
- [ ] Network sources stay replayable from the recorded cache, so runs remain deterministic
- [ ] Fixture carries precomputed fingerprints for its tracks, recorded like the network cache, since it holds no audio
- [ ] New baseline saved, and the drop from the old 1.0 figures is explained in the ticket as a fixture change rather than a regression
