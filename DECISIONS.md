# DECISIONS — unattended run

Record every call the user did not make. Narrowest reversible option, with alternatives rejected and how to reverse.

---

## D1 — Per-field LWW tie-breakers

**Question:** Concurrent writes inside same millis, skewed clocks, edit vs delete same entity.
**Taken:** LWW by updatedAt (unix millis). Tie → server timestamp wins over device; exact tie → deviceId lex order deterministic; delete sentinel `__deleted` wins over concurrent field edit.
**Rejected:** Vector clocks (heavier, no causality needed for per-field), last-writer-wins by revision alone (loses wall-clock intent).
**Reverse:** Swap `internal/sync/merge.go` MergePolicy implementation; seam is interface.
**Task:** T3

## D2 — Sync revision is server AUTOINCREMENT, not hybrid clock

**Question:** How to order global history?
**Taken:** `sync_change.revision INTEGER PRIMARY KEY AUTOINCREMENT` on server only. Devices never assign revisions; they pull `revision > cursor`.
**Rejected:** Client-assigned revisions (conflict on concurrent inserts), hybrid logical clocks (overkill for single-writer revision).
**Reverse:** Change `internal/store/migrations/0024` + `internal/sync/store.go`.
**Task:** T1/T3

## D3 — Offline set is local-only, never emits sync_change

**Question:** How to enforce "removing from offline set must not propagate"?
**Taken:** Separate `offline_set` table; mutations never write to `sync_change`; FK cascade on playlist deletion cleans rows.
**Rejected:** Flag on sync_change (would leak local intent), sync-filtered field (brittle).
**Reverse:** Add sync emission in `internal/offlineset/offlineset.go` if spec changes.
**Task:** T6

## D4 — Add-from-link runs on whichever device is chosen, result syncs to canonical library

**Question:** TASK: "Downloads run on whichever device is chosen, and the result always syncs back to the canonical library."
**Taken:** MVP: server enqueues download; `POST /links/add` creates/updates `catalog_entity` + enqueues via `DownloadManager`; completion emits `sync_change` for new track so devices learn it. Laptop-local jobs stay local until they sync (future proxy). No transcoding — spotDL args unchanged (`--audio youtube-music youtube`, no bitrate flag).
**Rejected:** Immediate cross-device job dispatch (needs device-to-device RPC), transcoding to 256k (violates source-native).
**Reverse:** Add device-targeted dispatch in `internal/linkresolve` + `internal/api/links.go`.
**Task:** T7

## D5 — Pairing code format & token storage

**Question:** Code usability vs security.
**Taken:** 8 chars [A-Z0-9] excluding I/O/0/1, displayed XXXX-XXXX, stored stripped; 10 min TTL, single-use. Token 32 random bytes base64url, stored as hex(sha256) only.
**Rejected:** Numeric 6-digit (too guessable), JWT (unnecessary, single DB lookup is fine).
**Reverse:** Change `internal/sync/pairing.go` generators.
**Task:** T2

## D6 — Device identity is `device` table, not reuse of `users`

**Question:** Reuse `users` (leftover from 263ac0f) or new table?
**Taken:** New `device` table. `users` stays as single local FK target (`local`) per `internal/auth`. Device is distinct concept (CONTEXT vocabulary).
**Rejected:** Overloading `users` (violates vocabulary, conflates local user with paired laptops).
**Reverse:** Migration to merge if needed.
**Task:** T1

