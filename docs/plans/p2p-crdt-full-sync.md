# Plan: True P2P CRDT — Full File Sync + Single-Code Pairing + libp2p

**Date:** 2026-08-28
**Status:** Approved — choices locked: `full file sync`, `single code UX`, `libp2p for remote`
**Prior doc:** supersedes hub-and-spoke `CONTEXT.md:18 Server` — every device is equal peer, converges while partitioned.

---

## 1. Goal / Non-goals

**Goal:** any two Reverb devices link with a single pairing code, discover each other on LAN (mDNS) and WAN (libp2p DHT + relay), and converge playlist/catalog/play history while fully replicating audio files — with no always-on server. Edits made offline on both sides converge deterministically when they next see each other.

**Non-goals (v1):**
- Replacing existing `subsonic`/`deezer`/`spotify`/`spotdl` adapters (`internal/library/library.go:14`, `internal/search/search.go`, `internal/download/download.go`) — they stay, a new `peer` source is added alongside.
- Encrypted-at-rest DB (still unencrypted `docs/deployment.md:177` — treat `device.token_hash` as secret).
- Mobile.

---

## 2. Decisions (locked)

| # | Question | Resolution | Rationale |
|---|----------|------------|-----------|
| **D1** | File scope | **Full replication** (`~/Music/Reverb` `desktop/paths.go:34` + `REVERB_DOWNLOAD_DIR` `wiring.go:216` fully synced). | User wants library identical everywhere; on-demand `GET /files/{id}` alone insufficient. |
| **D2** | Pairing UX | **Single code** — generator shows `XXXX-XXXX` (`pairing.go:71` `ABCDEFGHJKLMNPQRSTUVWXYZ23456789`, 10m TTL), redeemer inputs it alone. | Familiar flow; redeem now dials generator over libp2p relay instead of central `POST /pairing/redeem` `api/pairing.go:56`. |
| **D3** | Discovery / NAT | **libp2p** (Noise + QUIC, mDNS, Kademlia DHT, CircuitRelay v2, DCUtR, Identify, GossipSub) + keep plain HTTP `POST /sync` `api/sync.go:22` as LAN fallback. | `go.mod:1` has 0 p2p deps today — add `libp2p/go-libp2p`. Covers LAN + remote without port-forward. |
| **D4** | CRDT lib | Hand-roll **LWW-Register + OR-Set/RGA + HLC** on `sync_change` (`sync.sql:11 GetLatestSyncChangeForField`) rather than `Automerge-go`. | Pure Go, `modernc.org/sqlite v1.34.1` stays cgo-free for non-desktop, deterministic JSON already. |
| **D5** | Clock | **Hybrid Logical Clock** replaces wall `UpdatedAt` + `IsServer` tie-break (`merge.go:19` `IsServer` wins -> `hlc > wins else lex`). | Wall skew breaks LWW; HLC gives total order without server. |
| **D6** | Pinning | `offline_set(device_id,playlist_id)` (`0024:34`) reinterpreted as **pin set** (which playlists' files to keep). Previously `offlineset/offlineset.go:31 never syncs` local-only — becomes per-peer replicated OR-Set with GC. | Full sync would otherwise fill disk. |

---

## 3. Findings (current)

- **Star sync:** `0024_devices_sync_offline.sql:2-41` `device(is_server WHERE 1 unique 0026:2)`, `pairing_code`, `sync_change(revision AUTOINCREMENT, device_id, entity_type, entity_id, field, value_json, updated_at)`, `sync_cursor`, `offline_set` + `store.go:329 Reconcile` single-DB TX + `LWW+delete-wins` + `sync.go:34 LWWPolicy{IsServer}`. `api/sync.go:22` client->server star, `api/middleware_sync.go:16 authenticateSync` Bearer or `currentUser->ServerDeviceID`.
- **Catalog:** `0019:2` `catalog_entity(trk_/alb_/art_+idgen)`, `catalog_alias(isrc|external|norm)` `catalog/canonical.go:16`, `backend_binding(catalog_id, library_identity, backend_id)` `resolver/resolver.go:93`, merge on alias collision `catalog/merge.go:33 repointCanonicalRefs`.
- **Playlists:** `0006:2 synced_playlists tracks_json []ExternalResult` hybrid `mode=synced|once` (`0011:2`), `playlistsync/service.go:643 ReorderTracks` snapshot overwrite.
- **Plays/Downloads:** `0020 plays` insert-only G-Set, `0003 download_jobs canonical_id 0022:2` via `manager.go:361 mintAndStoreCanonicalID` after scan.
- **Network:** `cmd/reverb/main.go:46 :8090 0.0.0.0`, `desktop/main.go:92 127.0.0.1:0` loopback-only, `wails` `AssetServer` cannot hijack WS (`desktop/frontend.go:11`), `events/bus.go:5` in-process drop-on-full, `go.mod:1` no mDNS/libp2p.
- **Desktop data:** `~/Library/Application Support/Reverb/reverb.db` mac / `~/.config/reverb/reverb.db` linux `desktop/paths.go:20`, `~/Music/Reverb` downloads `desktop/paths.go:34`, `desktop/bundle.go:14` bundled tools.

---

## 4. Architecture

```
Device A (Wails)                          Device B (Docker/server)
┌──────────────┐  libp2p host + 0.0.0.0:0  ┌──────────────┐
│ SQLite        │◄─Noise/QUIC/Relay/DHT──►│ SQLite        │
│ sync_change{  │  /reverb/sync/1.0.0      │ sync_change   │
│  hlc, seq,    │  /reverb/file/1.0.0      │  (vector)     │
│  entity/field │  mDNS _reverb._tcp       │               │
│ } vector      │  GossipSub hlc head     │               │
│ catalog+alias │                         │ catalog+alias │
│ synced_pl     │                         │ synced_pl     │
│ plays G-Set   │                         │ plays         │
│ file_manifest │◄─want/have bitswap-lite►│ file_manifest │
├──────────────┤  fsnotify ~/Music/Reverb ├──────────────┤
│ Navidrome    │  Scan->library_version   │ Navidrome    │
│  embedded    │  ->backend_binding local │  embedded    │
└──────────────┘                         └──────────────┘
```

- **Host** `internal/p2p/host.go` started in `app/build.go:78 Build` alongside `bus := events.New()`, published via `reloader`-style `atomic.Pointer[peerSetHolder]` (`reload.go:42` pattern). `desktop/app.go:36 OnStartup` and `cmd/reverb/main.go:52` start it; `OnShutdown:74` closes.
- **Dual listener:** keep `127.0.0.1:0 LocalAPIPort` (`api/runtimeconfig.go:20 window.__REVERB_PORT__`) for Wails WS, add `lanPort` `0.0.0.0:0` for LAN HTTP fallback + libp2p QUIC. Advertise both in mDNS TXT + `identify`.
- **Adapters:** register `peer` search/library via `registry.NewRegistry` (`app/build.go:118`) — wiring `BuildSearchSources:128` / `BuildLibraryAdapter:56` treat `peer` as just another source peering via libp2p, fan-out already via `search/aggregator.go`.

---

## 5. Data Model Changes

**New `0029_p2p_hlc.sql`:**
- Add `sync_change.hlc INTEGER NOT NULL`, `seq INTEGER NOT NULL`, `file_manifest(canonical_id TEXT PK, content_hash TEXT NOT NULL, size INTEGER, rel_path TEXT, mtime INTEGER, device_id TEXT REFERENCES device(id))`, `sync_vector(device_id TEXT PK, seq INTEGER, hlc INTEGER)` replaces `sync_cursor` per-peer vector.
- Add `local_device_id TEXT` in `settings` (replaces `server_device_id` `sync/device.go:49`), generate `dev_<uuid>` once via `device.go:170` pattern.
- Fix `track_override.track_id PK` (`0027:5`) -> `catalog_id TEXT PK` (`api/openapi.yaml:117` rename) — otherwise backend-volatile IDs never converge.
- New indexes: `idx_sync_change_hlc(hlc, device_id)`, `idx_sync_change_device_seq(device_id, seq)`, `idx_file_manifest_hash(content_hash)`. *Drop* `0024:41 idx_device_single_server` (allow N `device` rows, deprecate `is_server` -> keep column but ignore).

**Clock:** `internal/sync/hlc.go:New()` -> `hlc = max(wallMilli, lastHLC)+1` per local append. `LWWPolicy.PickWinner` (`merge.go:19`) becomes `if hlc != {return hlc > }` `return deviceID <` (lex), delete `IsServer` injection (`store.go:300 effectivePolicy`).

**Query:** `sync/sql` `ListSyncChangesSince` `WHERE revision > ?` -> `WHERE vector > ? ORDER BY hlc` (`store.go:225 ListSince` vector arg, inbox cap `5000` `store.go:330` kept, outbound `LIMIT 10000` kept).

---

## 6. CRDT Per-Table

| Table | CRDT | Merge |
|-------|------|-------|
| `catalog_entity`+`catalog_alias` | Grow-only + alias-collision merge | Reuse `catalog/canonical.go:57 corroborates`, `catalog/merge.go:33` winner=older `created_at`, `repointCanonicalRefs` for alias/binding/plays/download canonical_id — change `idgen` to `deviceID+seq` deterministic, lex tie. |
| `synced_playlists` fields (`name, cover_url, sync_enabled, interval, auto_download, mode`) | LWW-Register per `field` | `sync.sql:GetLatestSyncChangeForField entity_type=playlist` stays, each field maps to `playlist/<id>/<field>` |
| `synced_playlists tracks_json` | **OR-Set / RGA** ordered list | Replace snapshot `ReorderTracks:643`/`AddTrack:530`/`RemoveTrack:592` single `field=tracks` overwrite with ops `op add{pos, entry{source,externalID,title,artist,isrc,duration}, dot}` `remove{dot}` `move{from,to}` stored as `entity_type=playlist_tracks`. Converges via RGA. |
| `plays` | G-Set insert-only | `plays.sql:7 INSERT` dedup by `id`, union on sync, no LWW (`play/service.go:56 Record`). |
| `download_jobs` | Local queue + shared completed history | Active `queued/running` `sqlstore.go:12 JobStore` stays local; only `completed+canonical_id` rows replicated (used by `DistinctDurableCanonicalIDs` `plays.sql:1` sweep `wiring.go:469`). Don't replicate `output_path`. |
| `scrobble_*` `sessions` `adapter_instances` `settings` `match_cache/album_coverage/backend_binding/library_version` | Local-only | Not synced — `backend_binding` re-resolves via `resolver/resolver.go:137 rematchAndStore` after file arrives. |

`offline_set` -> **pin OR-Set**: `entity_type=pin` replicated, `field=playlist_id` add wins; GC unpinned `file_manifest` files locally (don't delete canonical metadata).

---

## 7. Networking — libp2p

**Dep:** `github.com/libp2p/go-libp2p v0.36+` + `quic-go`, ~+15MB binary (`Makefile:23 build -tags prod`).

**Host** `internal/p2p/host.go:New()` -> `libp2p.New(libp2p.ListenAddrStrings("/ip4/0.0.0.0/udp/0/quic-v1","/ip4/0.0.0.0/tcp/0"), libp2p.Security(noise.ID, tls.ID), libp2p.Muxer(yamux.ID, mplex.ID), libp2p.NATPortMap(), libp2p.EnableRelay(), libp2p.EnableHolePunching())` + `mdns.NewMdnsService` `_reverb._tcp` + `kaddht.New` + `relayv2` + `dcutr` + `pubsub.NewGossipSub` topics `reverb/sync/1` (hlc head), `reverb/file/1` (want/have).

**Discovery:** LAN `mDNS` (replaces `grandcat/zeroconf`), WAN `DHT bootstrap` (`REVERB_BOOTSTRAP_PEERS` env `config/config.go:9`, default `libp2p bootstrap` + optional self-hosted rendezvous). Advertise `TXT id=<devID> hlc=<head> lanPort=<port>`; `PeerSyncer` (`internal/p2p/syncer.go`) maintains `atomic.Pointer[peerSet]` similar to `reload.go:42`.

**Transport:** keep `api/sync.go:22 POST /sync` HTTP for LAN fallback, add `protocolID /reverb/sync/1.0.0` framing same `SyncRequest{SinceVector map[deviceID]seq, Changes[]}` (`sync.go:3`) -> `Reconcile` vector. Fallback `GET /sync/status {hlc, vector, peerCount}` (`api/sync.go:69`).

**Lifecycle:** `app/build.go:296 StartBackground` starts `p2p.Host` + `Syncer.Run(ctx)` anti-entropy `30s` + on `local AppendChange` immediate push. `desktop/app.go:36 OnStartup/OnShutdown:74` lifecycle mirrors `Supervisor`/`Manager.Start/Stop`.

---

## 8. Pairing — Single Code over libp2p

Reuse `pairing.go:71 GenerateCode XXXX-XXXX 10m` + `pairing_code(code PK, expires_at)` (`0024:11`) local to generator only (not replicated).

Flow:
1. A: `POST /pairing/code` `api/pairing.go:32` (guard `CapManageLibrary`) -> write `code` stripped, return `XXXX-XXXX`. Display `code + peerID + relay addrs`.
2. B (redeemer, not yet authenticated, `POST /pairing/redeem` public `server.go:286`): inputs `code` -> libp2p `FindPeers` / relay dial `A` via `peerID` (code out-of-band proves knowledge) -> stream ` /reverb/pair/1.0.0` -> `PairingService.Redeem` tx `CreateDevice+TryMarkPairingCodeUsed` (`pairing.go:116-164` `WHERE used_at IS NULL`) enforces single-use, then exchange Noise certs + persist `peer{device_id, pubkey, peerID, addrs}` (`device.token_hash` `device.go:88` becomes `peerID` ed25519). Both sides pin cert (TOFU).
3. Return `{deviceId, token, serverDeviceId}` -> now `authenticateSync` `middleware_sync.go:16` accepts `Bearer <token>` via `AuthenticateByToken` `pairing.go:262` or `mTLS` peer cert.

Rate-limit + 10m TTL + single-use `TryMark` keeps `ABCDEFGHJKLMNPQRSTUVWXYZ23456789` 8-char entropy sufficient for local pairing. After, `GET /pairing/devices` `api/pairing.go:106` lists peers; `DELETE /pairing/devices/{id}` `pairing.go:123` now also closes libp2p conn + deletes `sync_vector` + `file_manifest` pins.

---

## 9. Full File Sync

**Manifest:** `file_manifest` derived from `fsnotify` watch on `REVERB_DOWNLOAD_DIR` vs `download_jobs.output_path` (`sqlstore.go:237`). On local `fsnotify.Create`, hash `sha256` streaming, `INSERT manifest`, `AppendChange entity_type=file` (or separate file topic). On `download/manager.go:361 mintAndStoreCanonicalID` success, same.

**Protocol:** lightweight want/have — peer advertises `Vector[device->seq]` + `manifest heads` via GossipSub, peer requests missing `content_hash` via ` /reverb/file/1.0.0` stream chunked `1MB`, `Range` resume, hash verify, write `REVERB_DOWNLOAD_DIR/<canonical>/<hash>`, then trigger existing `manager.go:1211 scheduleScan` (5s debounce) -> `StartScan` -> `library_version bump wiring.go:657` -> `resolver rematchAndStore` -> `backend_binding` local. `lyrics/*.lrc` and covers sync same path.

**Conflicts:** same `canonical_id` (same ISRC `catalog/canonical.go:16 isrc` or norm fingerprint) with different hashes — keep both, `catalog/merge.go:33` merge keeps winner alias, loser hash stays as alternate file (dedup by hash, not by id).

**GC/Quota:** pin set drives retention. Unpinned hashes after `offline_set` remove and no `synced_playlists` references -> local `DeleteFile` after 7d (`AsyncMaxAge 7d` `manager.go:606` pattern). UI `web/src/routes/Settings.tsx` quota slider + `internal/api/dist` bandwidth throttle.

---

## 10. Changes Summary

| Path | Action |
|------|--------|
| `internal/store/migrations/0029_p2p_hlc.sql` | new — hlc/seq/vector/manifest, drop server index, track_override fix |
| `internal/sync/hlc.go` | new — HLC clock |
| `internal/sync/store.go:300` `merge.go:19` | HLC+lex, vector Reconcile, remove IsServer |
| `internal/sync/device.go:49` | keep col, deprecate `EnsureServerDevice:99`, add `EnsureLocalDevice` |
| `internal/store/queries/sync.sql` `file.sql` `peers.sql` | vector queries, manifest queries |
| `internal/p2p/*` | new — `host.go`, `syncer.go`, `file.go`, `pairing.go` (libp2p transport) |
| `internal/app/build.go:78` `reload.go` | wire Host + Syncer, peerSet holder, dual listeners |
| `cmd/reverb/main.go:46` `desktop/main.go:92` | LAN port + host lifecycle |
| `internal/api/sync.go:22` `pairing.go:32` | vector payload, libp2p pairing endpoint |
| `internal/api/files.go` | new — `GET /files/{canonical_id}` + range |
| `web/src/lib/syncApi.ts` `pairingApi` `peersApi` | vector + pin + file sync status |
| `docs/plans/p2p-crdt-full-sync.md` | this file |

---

## 11. Verification

- `go test ./internal/sync -run TestSyncConvergencePartition` — two temp `store.Open` DBs, divergent `AppendChange` offline, mutual `Reconcile` vector -> identical `GetLatestForField` + `file_manifest` + `plays` union.
- `go test ./internal/catalog -run TestCatalogMergeDot` — alias collision with HLC dots converges.
- `go test ./internal/p2p -run TestPairSingleCode` — code single-use `TryMark` + relaid dial mock.
- E2E: two `wails dev`/`run_fallback.go:14` instances on loopback `127.0.0.1:0` + libp2p loopback, partition net, edit same playlist field, heal -> LWW HLC winner deterministic.
- `make gen` + `golangci-lint` (`errcheck, staticcheck`) + `make test` `go test ./cmd/... ./internal/... && web npm test`.

---

## 12. Rollout Order

1. **Phase 0 (this PR):** migration `0029`, `hlc.go`, `file_manifest`, `EnsureLocalDevice`, drop `idx_device_single_server` usage (no libp2p yet).
2. **Phase 1:** `LWW HLC` + vector `Reconcile` + tests, keep HTTP sync.
3. **Phase 2:** `internal/p2p/host.go` + mDNS/DHT/relay + `PeerSyncer` + `/reverb/sync` stream.
4. **Phase 3:** single-code pairing over libp2p.
5. **Phase 4:** file want/have + fsnotify + scan integration.
6. **Phase 5:** pin GC, quota UI, docs, `CONTEXT.md` topology rewrite.

---

## 13. Risks

| Risk | Mitigation |
|------|------------|
| libp2p binary/size (+15MB, QUIC) | Hide behind `//go:build p2p` tag for Docker small variant; desktop always on. |
| NAT without relay | Default libp2p public relay + `REVERB_RELAY` env; LAN mDNS still works offline. |
| Full sync disk explosion | Pin set + GC + quota before 4b ships; default pin=`recent + offline_set` only. |
| `127.0.0.1:0` firewall | LAN `0.0.0.0:0` prompts once; document `REVERB_LISTEN_ADDRESS`. |
| Clock HLC downgrade on restore from backup | `hlc` persisted + `max(wall,last)` handles backup replay. |


---

## 14. Peer trust model

Discovery is not trust. mDNS auto-connects to anything advertising `_reverb._tcp`,
and DHT/relay extends that to the internet, so an open connection says nothing
about who is on the other end. The `p2p_peer` table (migration `0031`) is the
trust set: a row is created only by completing a pairing-code exchange, and it
binds a libp2p peer ID to a `device` row.

- `/reverb/pair/1.0.0` is the one handler open to unpaired peers — it is how
  trust is bootstrapped. It is rate limited (5 attempts per peer, 30 global, per
  15 minutes) against brute force of the 2^40 code keyspace. Both sides record
  the other in `p2p_peer` on success.
- `/reverb/sync/1.0.0` and `/reverb/file/1.0.0` require a `p2p_peer` row for the
  connecting peer ID.
- Identity comes from the libp2p connection, never from the message body. A
  device ID travels in the author field of every change, so it is not a secret
  and cannot authenticate anyone. `SyncRequest.DeviceID` is honoured only when it
  matches the peer's bound device.
- **Changes carry the author's Ed25519 signature** (`sync_change.sig`,
  migration `0032`), so a relayed change can be verified without trusting the
  relay. A change authored by the sending peer is accepted on the strength of
  the authenticated connection; any other change must verify against its
  author's key. Transitive propagation therefore works: A and C converge through
  B without pairing directly.
- **Keys are the libp2p host identity.** Ed25519 peer IDs embed their public
  key, so pairing binds a verification key with no separate exchange, and
  `device.public_key` is just that key. The host key is persisted
  (`p2p_host_key` setting) — it must survive restarts, since a new key means a
  new peer ID and every existing pairing would break.
- **Device keys spread trust-on-first-use.** Peers gossip known device keys in
  `SyncRequest.Devices`/`SyncResponse.Devices`. The first key seen for a device
  wins and is never replaced: introducing an unknown device asserts nothing a
  peer could not have claimed under its own name, but rebinding a known
  device's key would be identity takeover and is refused.
- Every stream decoder reads through a byte cap (`internal/p2p/limits.go`), and
  peer file fetches require a content hash, land in a temp file, and are renamed
  into place only after the digest matches.
