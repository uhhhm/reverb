# PLAN — Multi-device Reverb (single binary, server = canonical library)

## Value ordering
Pairing + sync foundation land first (everything rests on them). Deletion propagation second. Offline set third. Add-from-link last. Every commit stays green on its own.

---

## Frozen vocabulary (CONTEXT.md — binding on identifiers, API paths, DB tables, UI copy, commits)

| Concept | Term | Avoid |
|---|---|---|
| Any running instance | **device** | client, node, peer |
| Always-on rendezvous | **server** | hub, host |
| Authoritative copy | **canonical library** | — |
| Granting a sync token | **pairing** (pairing code, sync token) | — |
| Local subset per-playlist | **offline set** | offline library, cache |
| Acquiring track/album | **download** | import, fetch |
| Paste URL → resolve → add | **add from link** | paste link, URL import |
| Store exactly as source | **source-native** | — |
| Reconciliation | **sync** (per-field most-recent-write-wins) | mirror, replicate |
| Remove from canonical library → all devices | **deletion** (propagates) | — |
| Remove from offline set | local-only, must not propagate | — |

Tie-breakers when glossary is silent (recorded in DECISIONS.md): server timestamp wins over device; delete wins over concurrent edit; device ID breaks exact ties deterministically. All behind a seam.

---

## Frozen interface contracts

### 1. Device / pairing schema (new migration 0024)

```sql
-- device: one row per paired laptop plus one row for the server itself.
CREATE TABLE device (
  id         TEXT PRIMARY KEY,          -- e.g. dev_ + uuid
  name       TEXT NOT NULL,             -- human label ("Allen's laptop")
  token_hash TEXT NOT NULL UNIQUE,      -- sha256 of sync token (server stores hash only)
  is_server  INTEGER NOT NULL DEFAULT 0,-- 1 for the canonical server device
  created_at INTEGER NOT NULL DEFAULT (unixepoch()),
  last_seen  INTEGER NOT NULL DEFAULT (unixepoch())
);
CREATE TABLE pairing_code (
  code       TEXT PRIMARY KEY,          -- 8-char alphanumeric, e.g. "A3K9-Q2P1" stored without dash
  expires_at INTEGER NOT NULL,          -- unix seconds, 10 min TTL
  used_at    INTEGER,                   -- NULL until consumed
  used_by_device_id TEXT REFERENCES device(id),
  created_at INTEGER NOT NULL DEFAULT (unixepoch())
);
-- Settings key: server_device_id = device.id of the server row
```

Go types (`internal/sync/pairing.go` owns these; `internal/store/db` generated):

```go
type Device struct { ID, Name, TokenHash string; IsServer bool; CreatedAt, LastSeen int64 }
type PairingCode struct { Code string; ExpiresAt int64; UsedAt *int64; UsedByDeviceID *string }
```

Token generation: 32 random bytes → base64url (43 chars) returned once to laptop; stored as hex(sha256(token)).
Code generation: 8 chars [A-Z0-9] excluding ambiguous, formatted XXXX-XXXX in UI, stored stripped. Single-use, 10 min expiry.

### 2. Sync protocol envelope (new package `internal/sync`)

#### Sync state tables (same migration 0024, continuation)

```sql
-- Global monotonic revision for the canonical library. Backed by settings key sync_revision (int64, starts 0, bumped per accepted change).
-- Sync changelog: every accepted mutation appends one row. Devices sync by pulling rows with revision > their cursor.
CREATE TABLE sync_change (
  revision   INTEGER PRIMARY KEY AUTOINCREMENT, -- server-assigned, monotonic
  device_id  TEXT NOT NULL REFERENCES device(id), -- originator (server or laptop)
  entity_type TEXT NOT NULL,   -- track | playlist | playlist_track | offline_set
  entity_id   TEXT NOT NULL,   -- catalog_entity.id or synced_playlists.id or composite
  field       TEXT NOT NULL,   -- e.g. title, artist, name, deleted, tracks_json | "__deleted" sentinel for deletion
  value_json  TEXT NOT NULL DEFAULT 'null', -- JSON-encoded new value (null for delete sentinel)
  updated_at  INTEGER NOT NULL, -- unix millis from originator; LWW key
  created_at  INTEGER NOT NULL DEFAULT (unixepoch())
);
CREATE INDEX idx_sync_change_revision ON sync_change(revision);
CREATE INDEX idx_sync_change_entity ON sync_change(entity_type, entity_id);

-- Per-device sync cursor (what revision the device has acknowledged).
CREATE TABLE sync_cursor (
  device_id TEXT PRIMARY KEY REFERENCES device(id),
  revision  INTEGER NOT NULL DEFAULT 0,
  updated_at INTEGER NOT NULL DEFAULT (unixepoch())
);

-- Tombstones for deletion wins semantics (optional fast path; sync_change with field="__deleted" is canonical).
-- No separate table; deletion is a sync_change row with field="__deleted" and value_json="true".
```

#### Envelope types (`internal/sync/sync.go`)

```go
package sync

type SyncRequest struct {
  // Auth: Authorization: Bearer <sync_token>  (validated against device.token_hash)
  // device identity derived from token; no deviceId in body needed.
  SinceRevision int64        `json:"sinceRevision"` // cursor the laptop last saw; 0 = full sync
  Changes       []SyncChange `json:"changes"`       // laptop's local mutations since last sync
}

type SyncChange struct {
  EntityType string `json:"entityType"` // "track" | "playlist" | "playlist_track"
  EntityID   string `json:"entityId"`
  Field      string `json:"field"`      // field name or "__deleted"
  Value      any    `json:"value"`      // JSON value; nil when Field=="__deleted"
  UpdatedAt  int64  `json:"updatedAt"`  // unix millis, originator wall clock
  DeviceID   string `json:"deviceId,omitempty"` // filled by server on response
  Revision   int64  `json:"revision,omitempty"` // filled by server on response
}

type SyncResponse struct {
  Changes      []SyncChange `json:"changes"`      // server changes with revision > SinceRevision (after merging inbound)
  NewRevision  int64        `json:"newRevision"`  // current global revision
  Accepted     int          `json:"accepted"`     // count of inbound changes accepted
  Rejected     []SyncChange `json:"rejected,omitempty"`
}

// Per-field LWW merge seam:
type MergePolicy interface {
  // PickWinner returns true if incoming wins over existing for same entity+field.
  PickWinner(existing, incoming SyncChange) bool
}
type LWWPolicy struct{} // implements: incoming.UpdatedAt > existing.UpdatedAt wins; tie → server wins, then deviceId lex order
```

API routes (added to `internal/api/server.go`, all under `/api/v1`, behind sync-token auth OR existing local auth where noted):

```
POST /pairing/code          -> {code, expiresAt}  (requires CapManageLibrary; rate-limited)
POST /pairing/redeem        -> {deviceId, token, serverDeviceId}  body {code, deviceName}
GET  /pairing/devices       -> []Device (admin list)
DELETE /pairing/devices/{id} -> {ok}
POST /sync                  -> SyncResponse  body SyncRequest  (auth: Bearer sync_token OR local cookie; token identifies device)
GET  /sync/status           -> {revision, deviceCount}
```

OpenAPI (`internal/api/openapi.yaml`) updated for each.

Sync semantics:
- Server is the only writer to `sync_change.revision` (AUTOINCREMENT). Laptops never assign revisions.
- On POST /sync, server applies inbound Changes via per-field LWW against last value for that entity+field (queried from latest sync_change row for that key). If incoming wins, insert new row (new revision). If loses, add to Rejected. Delete sentinel `__deleted` wins over concurrent field edits per DECISIONS.md. After applying, server returns all rows with revision > SinceRevision.
- Devices apply returned Changes locally (idempotent by revision). No revision gaps allowed; if gap detected, device re-syncs from 0.

### 3. Offline-set data model (same migration 0024)

```sql
CREATE TABLE offline_set (
  device_id   TEXT NOT NULL REFERENCES device(id),
  playlist_id TEXT NOT NULL REFERENCES synced_playlists(id) ON DELETE CASCADE,
  enabled     INTEGER NOT NULL DEFAULT 1, -- 1 = playlist is part of device's offline set
  updated_at  INTEGER NOT NULL,          -- unix millis for LWW if later synced (but offline_set is LOCAL-ONLY per CONTEXT)
  PRIMARY KEY (device_id, playlist_id)
);
-- NOTE: offline_set rows NEVER generate sync_change rows (local-only invariant).
-- Deletion of a playlist from canonical library (sync_change __deleted) cascades here via FK.
```

Go type (`internal/offlineset/offlineset.go`):

```go
type OfflineSetEntry struct { DeviceID, PlaylistID string; Enabled bool; UpdatedAt int64 }
```

API (local auth only; never sync-token; never emits sync_change):

```
GET    /offline-set              -> []{playlistId, enabled, playlistName}
PUT    /offline-set/{playlistId} -> {enabled} body {enabled: bool}
DELETE /offline-set/{playlistId} -> {ok}  (alias for enabled=false)
```

Frontend stores offline set in Zustand `offlineSetStore` + localStorage fallback; sync layer ignores it.

### 4. Add-from-link contract (package `internal/linkresolve`)

Supports Spotify and YouTube URLs. No transcoding — source-native (best available, 256kbps or as high as source offers) is already the spotDL behavior (`--audio youtube-music youtube`, no ffmpeg bitrate cap).

```go
// internal/linkresolve/resolver.go
type ResolveResult struct {
  Kind       string `json:"kind"`       // "track" | "album" | "playlist"
  Source     string `json:"source"`     // "spotify" | "youtube"
  ExternalID string `json:"externalId"`
  Title      string `json:"title"`
  Artist     string `json:"artist"`
  Album      string `json:"album"`
  CoverUrl   string `json:"coverUrl,omitempty"`
  URL        string `json:"url"`        // original URL
}

func ResolveURL(ctx context.Context, rawURL string) (*ResolveResult, error)
func ParseSpotifyURL(raw string) (kind, id string, ok bool)
func ParseYouTubeURL(raw string) (kind, id string, ok bool)
```

API:

```
POST /links/resolve  body {url: string} -> ResolveResult
POST /links/add      body {url: string, playlistId?: string, download?: bool} -> {resolve: ResolveResult, job?: DownloadJob, playlistId?: string}
  // If download=true (default), enqueues a download via DownloadManager.Enqueue using ManualURL when youtube, or normal spotify ExternalID.
  // Always inserts/updates catalog_entity for the resolved track so canonical library reflects it; sync_change emitted for new track/playlist membership.
```

Download runs on whichever device is chosen: if the caller is a laptop, the job is enqueued on that device's manager; the result syncs to canonical library via sync_change. MVP: server enqueues; laptop jobs are local (future: server can proxy). Document in DECISIONS.md.

No `client`/`import`/`fetch` in any identifier; use `linkresolve`, `ResolveURL`, `AddFromLink`.

---

## Tasks

### T1 — Schema & generated queries (foundation)
- **Owns:** `internal/store/migrations/0024_devices_sync_offline.sql`, `internal/store/queries/devices.sql`, `internal/store/queries/pairing.sql`, `internal/store/queries/sync.sql`, `internal/store/queries/offline_set.sql`, `internal/store/db/*.sql.go` (via `make gen`), `internal/store/migrate_single_user.go` (if needed for legacy).
- **Off limits:** `internal/sync/*`, `internal/api/*`, `web/*`
- **Depends on:** nothing
- **Work:** Create migration 0024 with tables above (device, pairing_code, sync_change AUTOINCREMENT, sync_cursor, offline_set). Add sqlc queries: CreateDevice, GetDeviceByTokenHash, GetDeviceByID, ListDevices, DeleteDevice, CreatePairingCode, GetPairingCode, MarkPairingCodeUsed, ExpirePairingCodes, AppendSyncChange, ListSyncChangesSince, GetSyncRevision, SetSyncCursor, GetSyncCursor, UpsertOfflineSet, ListOfflineSet, etc. Run `make gen`. Ensure `library_version` etc untouched.
- **Acceptance:** `gofmt -l ./cmd ./internal` empty; `go test ./cmd/... ./internal/...` passes; `make gen` idempotent.

### T2 — Pairing service (generate code + redeem)
- **Owns:** `internal/sync/pairing.go`, `internal/sync/pairing_test.go`, `internal/sync/device.go` (device helpers), `internal/store/db` (only via T1 queries)
- **Off limits:** `internal/api/*`, `web/*`, sync merge logic, offline_set
- **Depends on:** T1
- **Work:** `PairingService` with `GenerateCode(ctx) (code, expiresAt, error)` and `Redeem(ctx, code, deviceName) (deviceID, token, error)` (single-use, 10m TTL, constant-time code compare, token is 32 random bytes base64url, store sha256 hex). `DeviceService` helpers: `EnsureServerDevice`, `AuthenticateByToken`. Wire into `internal/wiring` composition root (create server device row on startup if absent). No `init()` side effects.
- **Acceptance:** `go test ./internal/sync/... -run TestPairing -v` passes; `go test ./cmd/... ./internal/...` passes.

### T3 — Sync core: per-field LWW merge + changelog
- **Owns:** `internal/sync/sync.go`, `internal/sync/merge.go`, `internal/sync/store.go`, `internal/sync/sync_test.go`
- **Off limits:** `internal/api/*`, `web/*`, pairing, offline_set, linkresolve
- **Depends on:** T1
- **Work:** Implement `LWWPolicy.PickWinner`, `SyncStore` (AppendChange, ListSince, GetRevision, cursor ops), and `Reconcile(ctx, deviceID, sinceRev, inbound []SyncChange) (outbound []SyncChange, newRev int64, rejected []SyncChange, err error)` that applies LWW + delete-wins + tie-breakers (server wins, then deviceID lex). Behind `MergePolicy` seam. Handle revision AUTOINCREMENT, concurrent writes inside same millis → tie-break. No HTTP yet.
- **Acceptance:** `go test ./internal/sync/... -run TestSync -v` passes including tie-break and delete-wins cases; `go test ./cmd/... ./internal/...` passes.

### T4 — Sync HTTP handlers + auth (server rendezvous)
- **Owns:** `internal/api/pairing.go`, `internal/api/sync.go`, `internal/api/middleware_sync.go` (or extend middleware.go), `internal/api/openapi.yaml` (pairing + sync sections), `internal/api/server.go` (route registration)
- **Off limits:** `web/*`, `internal/offlineset/*`, `internal/linkresolve/*`
- **Depends on:** T2, T3
- **Work:** Handlers: `POST /pairing/code`, `POST /pairing/redeem`, `GET /pairing/devices`, `DELETE /pairing/devices/{id}`, `POST /sync`, `GET /sync/status`. Middleware: `requireSyncToken` validates `Authorization: Bearer <token>` against device token_hash (constant-time), injects device into context; `/sync` accepts either sync token OR local cookie (server self-sync). Wire in `server.go` routes. Update `openapi.yaml`. Enforce CONTEXT vocabulary in API copy.
- **Acceptance:** `go test ./internal/api/... -run TestPairingAPI|TestSyncAPI -v` passes; `gofmt` clean; `go test ./cmd/... ./internal/...` passes.

### T5 — Deletion propagation (tombstones, delete-wins)
- **Owns:** `internal/sync/deletion.go`, `internal/api/deletion_test.go` (or extend sync tests), plus edits to `internal/api/library.go` and `internal/api/synced_playlists.go` (delete handlers emit sync_change), `internal/playlistsync/service.go` (delete path)
- **Off limits:** `web/*` (except maybe test), offline_set, linkresolve
- **Depends on:** T3, T4
- **Work:** Ensure deleting a playlist or track from canonical library writes a `__deleted` sync_change (tombstone) and that sync propagation deletes it on every device. Removing a track from offline_set must NOT emit sync_change (assert). Implement `ApplyDeletion` helper used by both playlist and track delete handlers. Cover concurrent edit-vs-delete (delete wins).
- **Acceptance:** `go test ./internal/sync/... ./internal/api/... -run TestDeletion -v` passes; offline-set removal does not produce sync_change (negative test); `go test ./cmd/... ./internal/...` passes.

### T6 — Offline set service + API (per-playlist, local-only)
- **Owns:** `internal/offlineset/offlineset.go`, `internal/offlineset/offlineset_test.go`, `internal/api/offline_set.go`, `internal/api/offline_set_test.go`
- **Off limits:** `web/*`, sync merge, linkresolve, pairing
- **Depends on:** T1
- **Work:** `OfflineSetService` CRUD against `offline_set` table (no sync emission). Handlers `GET /offline-set`, `PUT /offline-set/{playlistId}`, `DELETE /offline-set/{playlistId}` (local auth only). Validate playlist exists. On canonical playlist deletion, FK cascade removes offline_set rows. Explicitly test local-only invariant (no sync_change rows after offline set mutation).
- **Acceptance:** `go test ./internal/offlineset/... ./internal/api/... -run TestOfflineSet -v` passes; `go test ./cmd/... ./internal/...` passes.

### T7 — Add-from-link resolver + download wiring (source-native)
- **Owns:** `internal/linkresolve/resolver.go`, `internal/linkresolve/spotify.go`, `internal/linkresolve/youtube.go`, `internal/linkresolve/resolver_test.go`, `internal/api/links.go`, `internal/api/links_test.go`
- **Off limits:** `web/*`, offline_set, sync handlers
- **Depends on:** T3 (for sync emission after resolve) — can run in parallel with T6
- **Work:** Implement URL parsing for Spotify (track/album/playlist) and YouTube (video/playlist) without new credentials (reuse existing Spotify client if configured; YouTube via oembed/metadata fetch, no transcoding). `POST /links/resolve` and `POST /links/add` (resolve → ensure catalog_entity → optionally enqueue download via DownloadManager with source-native quality → emit sync_change for new track/playlist membership). Ensure spotDL adapter stays source-native (no ffmpeg bitrate flag). Register resolvers at composition root, no `init()`.
- **Acceptance:** `go test ./internal/linkresolve/... ./internal/api/... -run TestLinkResolve -v` passes; `go test ./cmd/... ./internal/...` passes.

### T8 — Wiring & composition root (server bootstrap, live reload)
- **Owns:** `internal/wiring/wiring.go`, `cmd/reverb/main.go` (device/sync bootstrap), `internal/wiring/reload.go` (if needed), `internal/store/db` (read-only usage)
- **Off limits:** `web/*`, handler files owned by other tasks
- **Depends on:** T2, T3, T4, T6, T7
- **Work:** Wire Device/Pairing/Sync/OfflineSet/LinkResolve services at startup; ensure server device row exists; expose them via `api.Deps`; make `ServiceReloader.Reload` rebuild them without restart. Ensure no `init()` registrations.
- **Acceptance:** `go test ./cmd/... ./internal/...` passes; `make build` succeeds (run once, not per task).

### T9 — Frontend: Pairing screens (admin code + laptop redeem)
- **Owns:** `web/src/routes/Pairing.tsx`, `web/src/routes/Pairing.test.tsx`, `web/src/lib/pairingApi.ts`, `web/src/lib/pairingApi.test.ts`, `web/src/App.tsx` (route add), `web/src/components/*` (only new pairing components)
- **Off limits:** `internal/*`, offline-set routes, add-from-link route
- **Depends on:** T4
- **Work:** Admin route `/pairing` (server): "Generate pairing code" button → shows XXXX-XXXX code + expiry countdown + copy. Laptop flow: `/pairing` when not paired shows "Enter pairing code" input → POST /pairing/redeem → store sync token in localStorage (`reverb:syncToken`) + deviceId. No `client`/`hub` in UI copy — use "device" / "server" / "pairing code". Co-located vitest tests. Update OpenAPI-driven types.
- **Acceptance:** `cd web && npm run test -- src/routes/Pairing.test.tsx` passes; `npm run lint` passes.

### T10 — Frontend: Offline set (per-playlist) + sync status
- **Owns:** `web/src/routes/OfflineSet.tsx` (or extend Library/Playlist routes), `web/src/lib/offlineSetApi.ts`, `web/src/lib/offlineSetStore.ts`, `web/src/lib/syncApi.ts`, `web/src/lib/syncStore.ts`, plus edits to `web/src/routes/SyncedPlaylist.tsx` (offline toggle per playlist) and `web/src/components/*` (new offline toggle)
- **Off limits:** `internal/*`, pairing route, add-from-link route
- **Depends on:** T6
- **Work:** Per-playlist "Keep offline" toggle (calls PUT /offline-set/{id}). Offline set page lists offline playlists. Sync status indicator (revision, last sync) via GET /sync/status + realtime bus. Deleting offline track is local-only (no sync). Tests co-located.
- **Acceptance:** `cd web && npm run test -- src/lib/offlineSetApi.test.ts src/routes/OfflineSet.test.tsx` passes; `npm run lint` passes.

### T11 — Frontend: Add-from-link (paste URL → resolve → add/download)
- **Owns:** `web/src/routes/AddFromLink.tsx`, `web/src/routes/AddFromLink.test.tsx`, `web/src/lib/linkApi.ts`, `web/src/lib/linkApi.test.ts`, `web/src/App.tsx` (route add), `web/src/components/AddFromLinkForm.tsx` (if needed)
- **Off limits:** `internal/*`, pairing/offline routes
- **Depends on:** T7
- **Work:** Route `/add` (or `/add-from-link`): input for Spotify/YouTube URL → POST /links/resolve preview → "Add to playlist" selector + "Download now" checkbox → POST /links/add → toast + navigate. Download runs on chosen device; result syncs to canonical library (poll sync status). Copy uses "add from link", "download", "source-native" exactly. Tests + lint.
- **Acceptance:** `cd web && npm run test -- src/routes/AddFromLink.test.tsx` passes; `npm run e2e` passes for add-from-link flow (mocked); `npm run lint` passes.

### T12 — E2E & conformance gate
- **Owns:** `web/e2e/pairing.spec.ts`, `web/e2e/offline-set.spec.ts`, `web/e2e/add-from-link.spec.ts`, plus any `internal/*conformance_test.go` updates
- **Off limits:** implementation files (read-only verification)
- **Depends on:** T9, T10, T11
- **Work:** Playwright specs mocking pairing, offline-set, add-from-link flows; verify sync propagation after add-from-link (mocked SSE/WS). Ensure existing conformance suites still pass. Run full gate.
- **Acceptance:** `go test ./cmd/... ./internal/...` passes; `cd web && npm run lint && npm run test` passes; `npm run e2e` passes (hermetic); `make build` passes.

---

## File ownership summary (one file, one task)

- T1: `internal/store/migrations/0024_*`, `internal/store/queries/devices.sql`, `pairing.sql`, `sync.sql`, `offline_set.sql`, `internal/store/db/*`
- T2: `internal/sync/pairing.go`, `device.go`
- T3: `internal/sync/sync.go`, `merge.go`, `store.go`
- T4: `internal/api/pairing.go`, `sync.go`, `middleware_sync.go`, `openapi.yaml`, `server.go` (route section)
- T5: `internal/sync/deletion.go`, edits to `library.go`/`synced_playlists.go` delete paths
- T6: `internal/offlineset/*`, `internal/api/offline_set.go`
- T7: `internal/linkresolve/*`, `internal/api/links.go`
- T8: `internal/wiring/*`, `cmd/reverb/main.go`
- T9: `web/src/routes/Pairing.*`, `web/src/lib/pairingApi.*`
- T10: `web/src/lib/offlineSet*`, `syncApi*`, edits to `SyncedPlaylist.tsx`
- T11: `web/src/routes/AddFromLink.*`, `web/src/lib/linkApi.*`
- T12: `web/e2e/*`, conformance verification

If two tasks need same file, merge tasks or split file (e.g., `server.go` route blocks are additive; T4 owns the sync/pairing route block, T6 owns offline-set block, T7 owns links block — each adds a distinct `Route` group).

---

## Dependency graph

```
T1 ─┬─> T2 ──> T4 ─┬─> T5
    ├─> T3 ──> T4 ─┤
    ├─> T3 ────────┘
    └─> T6 (offline) ──> T10
        T3 ──> T7 (links) ──> T11
              T2,T3,T4,T6,T7 ──> T8
        T4 ──> T9
        T6 ──> T10
        T7 ──> T11
        T9,T10,T11 ──> T12
```

Parallel batches (max 3):
- Batch 1: T1 alone
- Batch 2: T2, T3, T6 (T6 independent of T2/T3 except T1)
- Batch 3: T4 (needs T2+T3), T7 (needs T3) — can parallelize T4 and T7 after T3
- Batch 4: T5, T8, T9 (T5 needs T4, T8 needs T2-4+6+7, T9 needs T4)
- Batch 5: T10, T11
- Batch 6: T12

---

## Implementation notes for subagents

- TDD: RED commit `test(scope): Tn ...` then GREEN. Keep suites green.
- No `init()` side-effects; register adapters/services at composition root (`internal/wiring`).
- `make gen` after editing `internal/store/queries/*.sql`.
- Keep `internal/api/openapi.yaml` in sync with handler changes.
- Conventional Commits with task ID: `feat(sync): T4 per-field LWW merge` etc. Commit on `main`, never create branches.
- `gofmt -w` before committing. Gate: `gofmt -l ./cmd ./internal && go test ./cmd/... ./internal/... && cd web && npm run lint && npm run test`.
- Never `git add -A`; stage by explicit path; leave `Dockerfile`, `docker-compose.yml`, `.agents/`, `.claude/`, `CONTEXT.md`, `TASK.md` alone.
- Frontend: `*.test.tsx` co-located beside each route, matching repo pattern. No browser driving; rely on vitest + Playwright hermetic.

