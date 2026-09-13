# 12: Tracking recommendation results

**What to build:** Plays that came from a recommendation record which surface they came from (Radio, a Mix, a shelf, similar tracks). The Stats page gains a Recommendations section with skip rate, completion rate, and the rate of recommended tracks added to the library or a playlist, per surface over time.

**Blocked by:** 03

**Status:** done

- [x] Play records carry an optional origin, which replicates with the play. Older plays and peers without the field still work
- [x] Adding a recommended track to a playlist or the library is attributed to the surface it came from
- [x] Stats shows per-surface skip, completion, and add rates for a chosen time range
- [x] Migration, sqlc, and contract regeneration pass their drift checks
