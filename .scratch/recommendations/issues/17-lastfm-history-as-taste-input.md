# 17: Last.fm history as taste input

**What to build:** When a Last.fm account is linked for scrobbling, its listening history (top artists and tracks, and recent scrobbles from before Reverb) feeds the taste profile. This gives a new install a rich profile immediately.

**Blocked by:** 08

**Status:** done

- [x] Imported history is taste input only. It never creates plays and never replicates as plays
- [x] Scrobbles Reverb itself sent aren't double-counted
- [x] Unlinking Last.fm removes its contribution to the profile
- [x] The quality test with Last.fm history is no worse than without

## Comments

- Import lives in `internal/tastehistory`, reading `lastfm.History` (unsigned calls with the scrobbling API key). Rows go to the local `taste_history` table and reach the profile as `SignalHistory`. Importing happens on linking and weekly after, only while online recommendations are on.
- Each listen is counted once. Scrobbles from before the link are dated rows. Top tracks keep only the listens that are neither dated nor sent by Reverb, and top artists keep only those not sent by Reverb. Reverb's own scrobbles are the `done` rows in `scrobble_queue`.
- Weight is 1 quarter per doubling of listens. One listen counts as a quarter of a completed play, and 1000 listens as about 2.5 plays. At 2 quarters, two old listens equalled a full Reverb play.
- Reverb's own scrobbles are known only on the device that sent them, because the queue doesn't replicate. If a paired device scrobbles to the same account, its plays arrive once as replicated plays and again in this device's imported top counts.
- Scrobbles Reverb sent are subtracted per provider, not per account. After relinking to a different Last.fm account, the old account's sends are subtracted too. This can only undercount.
- Linking or unlinking changes the taste profile on its next build. Home shelves and Mixes already stored keep their ranking until their normal refresh.
- Linking removes any earlier history at once, then imports. Relinking during an import makes the running import read again. An import whose account changed while it was reading is thrown away.
- History stays on the device holding the Last.fm link and does not replicate, so a device without the link ranks without it. This is an exception to "devices with the same data rank identically".
- Quality: `TestLastfmHistoryIsNoWorseThanWithout` shows Radio and similar tracks unchanged on the fixture. The fixture has no real Last.fm data. A history row naming a proposed track that is absent from the held-out plays lowers Radio NDCG@10 (0.983 for a track with 2 listens), because history is meant to lift it. The written history leaves such rows out.
