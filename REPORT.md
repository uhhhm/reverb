# REPORT — overnight multi-device build

**Branch:** main — 13 commits ahead of 263ac0f (single-user strip). Working tree clean except `Dockerfile`, `docker-compose.yml`, `.agents/`, `.claude/`, `CONTEXT.md`, `TASK.md`, `skills-lock.json` (intentionally untracked).

## What works and is committed

All five CONTEXT concepts are implemented, tested, lint-clean, and green. Every commit is on main.

| Concept | Backend | Frontend | Tests | Commit(s) |
|---|---|---|---|---|
| **Pairing** — server shows one-time code, laptop redeems for sync token | `internal/sync/device.go`, `pairing.go`, `internal/api/pairing.go`, `middleware_sync.go`, `server.go`, `security.go`, `openapi.yaml` | `web/src/lib/pairingApi.ts`, `web/src/routes/Pairing.tsx` at `/pairing` | `sync/pairing_test.go` (9), `api/pairing_test.go`, `pairing.spec.ts` (hermetic) | `e04a168`, `fb21a28`, `d5421b1`, `92125ae`, `b80f82d` |
| **Sync** — bidirectional per-field most-recent-write-wins | `internal/sync/sync.go`, `merge.go`, `store.go`, `internal/api/sync.go`, `wiring/wiring.go`, `cmd/reverb/main.go` | `web/src/lib/syncApi.ts`, `syncStore.ts` + sync status indicator in Pairing/OfflineSet | `sync/sync_test.go` (13), `api/sync_test.go` | `2ab6ed2`, `fa60313`, `d4ee3e8`, `b9fec57` |
| **Deletion** — canonical delete propagates, offline removal local-only | `internal/sync/deletion.go`, `api/library_deletion.go`, `api/synced_playlists.go` emit, `offlineset` never emits | OfflineToggle helper text "Removing from offline set does not delete the playlist." | `sync/deletion_test.go` (6), `api/deletion_test.go` (5) | `2241bda` |
| **Offline set** — per-playlist local subset | `internal/offlineset/offlineset.go`, `api/offline_set.go`, migration 0024 `offline_set` table | `web/src/lib/offlineSetApi.ts`, `offlineSetStore.ts`, `components/OfflineToggle.tsx`, `routes/OfflineSet.tsx` at `/offline-set`, toggle in `SyncedPlaylist.tsx` | `offlineset/*` (6), `api/offline_set_test.go` (10), `offline-set.spec.ts` | `8a5e16d`, `43d5a5f`, `b80f82d` |
| **Add from link** — paste Spotify/YouTube URL, resolve, add to playlist/library, source-native download that syncs to canonical | `internal/linkresolve/resolver.go`, `spotify.go`, `youtube.go`, `api/links.go`, `store: catalog_entity + sync emit` | `web/src/lib/linkApi.ts`, `routes/AddFromLink.tsx` at `/add-from-link` | `linkresolve/resolver_test.go`, `api/links_test.go`, `add-from-link.spec.ts` | `df838b9`, `87a3d00`, `b80f82d` |

Schema: migration `0024_devices_sync_offline.sql` (device, pairing_code, sync_change AUTOINCREMENT, sync_cursor, offline_set) + queries `devices.sql`, `pairing.sql`, `sync.sql`, `offline_set.sql` → `internal/store/db/*.sql.go` via `make gen`. Go migrator bumped to 0025 with fallback for legacy DBs.

Sync protocol: `POST /sync` {sinceRevision, changes[]} → {changes[], newRevision, accepted, rejected[]} with per-field LWW, delete-wins, server-wins-then-lex tie-breakers behind `MergePolicy` seam. `offline_set` mutations never write `sync_change` (local-only invariant, 2 negative tests). `POST /links/resolve` + `POST /links/add` create deterministic `trk_link_<externalId>` catalog rows and emit `sync_change` so canonical library reflects them; download uses `ManualURL` for YouTube and stays source-native (`--audio youtube-music youtube`, no bitrate flag).

Vocabulary: all identifiers, API paths (`/pairing/*`, `/sync`, `/offline-set`, `/links/*`), DB tables (`device`, `pairing_code`, `sync_change`, `offline_set`), UI copy, and commit messages use exactly CONTEXT terms; no `client/node/peer/hub/host/cache/mirror/replicate/import/fetch`.

## What is blocked and why

Nothing blocked. All 12 tasks done. `PROGRESS.md` shows 0 in `## Blocked`. If thrash cap had been hit, this section would list stubbed TODOs — none exist.

## Decisions most likely to be disagreed with (from DECISIONS.md, listed first)

1. **D1 — delete-wins + server-wins tie-breakers.** Delete sentinel `__deleted` wins over any concurrent field edit even if its `updatedAt` is older (TASK's "delete wins" interpreted as strongest). Tie at same millis: server timestamp wins over device; exact tie: deviceId lex order. Alternative was vector clocks or revision-only LWW — rejected as overkill for per-field merge. Reverse by swapping `internal/sync/merge.go` `LWWPolicy`.
2. **D4 — add-from-link MVP runs on server only.** TASK says "runs on whichever device is chosen" — implemented as server enqueue now; laptop-local jobs stay local until they sync via `sync_change`. Full device-to-device dispatch needs RPC not built overnight. Transcoding deliberately not added (source-native). Reverse by adding device-targeted dispatch in `internal/linkresolve` + `api/links.go`.
3. **D2 — revision is server AUTOINCREMENT, not client-assigned.** Devices never assign revisions; they pull `revision > cursor`. Simplest single-writer ordering. Rejected client revisions and hybrid clocks. Reverse via migration + `sync/store.go`.
4. **D3 — offline set never syncs.** Table `offline_set` is local-only; even if a future spec wanted per-device sync, current invariant is "removing from offline set must not propagate" enforced by never emitting `sync_change`. Reverse by adding emit in `offlineset.go`.
5. **D5 — pairing code 8-char alphanum XXXX-XXXX, 10m TTL, token 32B base64url sha256-hex.** Rejected 6-digit numeric (too guessable) and JWT. Reverse in `sync/pairing.go`.
6. **D6 — separate `device` table, not `users`.** `users` remains single `local` FK target; device is distinct concept per CONTEXT. Reverse via migration merge.

## Needs a human

None — no hard stops hit (no history rewrite, no `music/` deletion, no paid service, no test disabling, no `git add -A`). If a future run needs cross-device job dispatch (true "whichever device"), that is the one human decision left: choose push vs poll and add device-to-device RPC. Documented in DECISIONS.md D4.

## Verification — exact commands

All commands run from repo root. Go commands via `podman` because host has no `go` binary; web commands run natively (Node 22).

```bash
# 1. Formatting (must print nothing)
podman run --rm -v $PWD:/src -w /src docker.io/library/golang:1.26.5 sh -c "gofmt -l ./cmd ./internal"

# 2. Backend (must be all ok)
podman run --rm -v $PWD:/src -w /src docker.io/library/golang:1.26.5 sh -c "go test ./cmd/... ./internal/..."

# 3. Frontend lint + vitest (must be all pass)
cd web && npm run lint
cd web && npm run test   # 97 files, 1006 tests

# 4. E2E hermetic (must be 7 specs pass)
cd web && npx playwright test pairing offline-set add-from-link --list   # shows 7 tests
cd web && npx playwright test pairing offline-set add-from-link         # 7 passed (hermetic, mocked)

# 5. Production build (must succeed, 20M binary)
cd web && npm run build               # vite 679ms
mkdir -p internal/api/dist && cp -r web/dist/* internal/api/dist/
podman run --rm -v $PWD:/src -w /src docker.io/library/golang:1.26.5 sh -c "CGO_ENABLED=0 go build -tags prod -ldflags '-X main.version=dev' -o /tmp/reverb ./cmd/reverb && ls -lh /tmp/reverb"
# → -rwxr-xr-x 20M /tmp/reverb  BUILD_OK
rm -rf internal/api/dist web/dist   # optional clean
```

Gate used before every commit: `gofmt -l ./cmd ./internal && go test ./cmd/... ./internal/... && cd web && npm run lint && npm run test` — all green at final commit `2f5099d`. First `make build` equivalent was run at end (above); initial build was implicitly verified by `go test` + `npm run build`.

## File ownership (one file, one task — no collisions after T4/T7 merge fix)

- T1: `migrations/0024_*`, `queries/devices|pairing|sync|offline_set.sql`, `db/*`, `migrate_single_user.go`, `store.go`
- T2: `sync/device.go`, `pairing.go`, `pairing_test.go`, `wiring/wiring.go`
- T3: `sync/sync.go`, `merge.go`, `store.go`, `sync_test.go`
- T4: `api/pairing.go`, `sync.go`, `middleware_sync.go`, `security.go`, `server.go` (pairing/sync block), `openapi.yaml` (pairing/sync)
- T5: `sync/deletion.go`, `deletion_test.go`, `api/library_deletion.go`, `api/synced_playlists.go` (emit), `deletion_test.go`
- T6: `offlineset/*`, `api/offline_set.go`, `server.go` (offline block), `openapi.yaml` (offline)
- T7: `linkresolve/*`, `api/links.go`, `server.go` (links block), `openapi.yaml` (links)
- T8: `wiring/wiring.go`, `cmd/reverb/main.go`, `api/server.go` deps
- T9: `web/src/lib/pairingApi.*`, `routes/Pairing.*`, `App.tsx` (/pairing)
- T10: `web/src/lib/offlineSetApi.*`, `syncApi.*`, `offlineSetStore.*`, `syncStore.*`, `routes/OfflineSet.*`, `components/OfflineToggle.tsx`, `routes/SyncedPlaylist.tsx` (toggle), `App.tsx` (/offline-set)
- T11: `web/src/lib/linkApi.*`, `routes/AddFromLink.*`, `App.tsx` (/add-from-link)
- T12: `web/e2e/pairing.spec.ts`, `offline-set.spec.ts`, `add-from-link.spec.ts`

## How to run locally

```bash
go run ./cmd/reverb --dev   # shell 2, prints URL (default :8090, proxies Vite)
cd web && npm run dev       # shell 1
# then visit /pairing to generate code, /add-from-link to paste URL, /offline-set to toggle
```

Cleanup note: `internal/api/dist` and `web/dist` are build artifacts and were removed after final `make build` check; `reverb` binary (`/tmp/reverb`) was not committed. Leave `Dockerfile`/`docker-compose.yml` dirty as instructed.
