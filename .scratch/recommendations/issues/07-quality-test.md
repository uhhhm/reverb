# 07: Quality test

**What to build:** A repeatable test of recommendation quality. It takes the play history, hides the most recent period, generates recommendations from what remains, and reports how many of the hidden plays were predicted (hit rate and ranking metrics such as recall@k and NDCG). It can run against the real local database or a checked-in anonymised fixture. It sets the baseline that ranking must beat.

**Blocked by:** 03

**Status:** ready-for-agent

- [ ] Runs as a make target or command against the fixture by default, or a given database
- [ ] Network sources can be replayed from a recorded cache so runs are deterministic
- [ ] Reports metrics per surface (Radio, similar tracks) and saves a baseline file to compare against
- [ ] The fixture contains no personal data beyond what's needed (anonymised IDs are fine)
