# 24: Taste profile must not miss a play when a rowid is reused

**What to build:** The incremental taste profile could permanently ignore a play when the plays table reuses a rowid. It folds plays by rowid and only rebuilds when the stored qualified-play count drops. Because the plays table has a text primary key and no `AUTOINCREMENT`, SQLite assigns `max(rowid)+1`, so deleting the highest-rowid play and then recording another before the next profile build gives the new play the deleted play's rowid. The fold then sees nothing after its high-water mark and the count is unchanged, so no rebuild happens and the play is absent from taste until some later net removal forces one.

**Blocked by:** none

**Status:** ready-for-agent

Evidence (verified at `b18b9a4`):

- `internal/recommend/taste.go:322-356` — `foldPlays` pages `ListTastePlaysAfter(seq)` and rebuilds only when `PlayCount() < st.count`.
- `internal/store/db/taste.go` — `ListTastePlaysAfter` uses `WHERE p.rowid > ? ORDER BY p.rowid`.
- `internal/store/migrations/0020_plays.sql:3` — `id TEXT PRIMARY KEY` (no `AUTOINCREMENT`), so rowids are reusable; reproduced directly with sqlite3 (delete max row, insert another, the new row reuses the deleted rowid).
- `docs/architecture.md` — plays are "folded in incrementally by rowid and rebuilt when one is removed"; the reuse case defeats that invariant.

Acceptance criteria:

- [ ] After deleting the most recent qualified play and recording another before the next profile build, the new play is reflected in the taste profile.
- [ ] The existing incremental fold and rebuild-on-removal behaviour, and cross-device determinism, are unchanged.
- [ ] A regression test reproduces rowid reuse (delete the max qualified play, insert a new play, fold again) and asserts the new play's signal is present.
- [ ] `go test ./internal/recommend/...` passes.
