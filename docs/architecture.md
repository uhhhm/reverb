# Architecture reference

Read the relevant section when changing adapter wiring, metadata identity, replication, playback transport, or desktop startup. Domain terminology is defined in [CONTEXT.md](../CONTEXT.md).


Go modular monolith — single binary, React SPA embedded at build time (`-tags prod` via `internal/api/embed.go`; `--dev` proxies Vite). Both entry points (`cmd/reverb` server and `desktop/` Wails) share one composition root in `internal/app/build.go` (`Build` wires, `StartBackground` starts) so dependencies are wired once; entry points only own how they listen/shut down. Hot-reload of adapters happens live via `internal/app/reload.go` (`ServiceReloader`) with no restart.

### Adapter/seam pattern (the core design)

Three pluggable seams, same shape: interface + adapters + conformance suite + explicit registry, **no `init()` side-effects**.

- **`library`** — `internal/library/library.go` (`LibraryAdapter`); adapters `internal/library/subsonic` + `internal/library/embedded` (built-in vs external, see below); `conformance.go`. The backend serves library records; Reverb persists catalog identity, bindings, metadata overrides, and materialized sync state in SQLite.
- **`search`** — `internal/search/search.go` (`SearchSource`); adapters `internal/search/deezer` (keyless), `internal/search/spotify`; `aggregator.go` fans out to all enabled sources concurrently over SSE with per-source `Envelope{Status,Results,Error}`; `conformance.go`. Optional caps via type assertion: `DiscographyProvider`, `TrackProvider`, `PlaylistProvider`, `PlaylistSearchProvider`.
- **`downloader`** — `internal/download/download.go` (`Downloader` + `DownloaderEntry{Order}` for per-instance granularity); adapters `internal/download/spotdl`, `internal/download/lidarr`, `internal/download/ytdlp`; `conformance.go`; `download/manager.go` owns queue/workers/dedup-join/fallback/scan-debounce/cancel/retry. Optional caps via type assertion: `AsyncDownloader` (Submit/Poll, reconciler lane) and `ChapterLister`.

Registration: `internal/registry/registry.go` holds constructors by name; `internal/app/build.go` registers `subsonic` / `spotify`+`deezer` / `spotdl`+`lidarr`+`ytdlp` explicitly, plus `registry.RegisterCapability("async", ...)`. `internal/wiring/wiring.go` (`Builder`) builds the active `ServiceBundle` from enabled `adapter_instance` DB rows; `internal/app/reload.go` rebuilds it on HTTP adapter mutations (`internal/api` adapter handlers) and publishes one atomic snapshot of request services, matcher, and track lookup so HTTP, `resolver`, and `extstream` read the active configuration. The application runtime serializes reload with shutdown, starts the candidate manager, publishes it, stops the old manager, and redispatches its requeued jobs. A build failure leaves the active snapshot intact. HTTP only requests reload and reads `Current()`; it owns no worker lifecycle. Requests that already captured an old service may finish while its workers retire.

Library modes (`internal/library/embedded`): **built-in** bundles Navidrome as a supervised child process against the same music dir (waveform-peaks via local file access); **external** points at a user-provided Subsonic/Navidrome. Mode is boot-bound (restart to change).

### Composition & control flow

- `internal/app/build.go` — composes `auth`, `catalog`, `resolver`, `download.Manager`, `playlistsync`, `scrobble`, `extstream`, `api.Deps`, `sync`, `p2p`; entry points call `Build` then `StartBackground` (supervisor, manager, backfill, sync scheduler, scrobble worker, p2p host + sync/file handlers).
- `internal/core` — domain types (`Artist`/`Album`/`ExternalResult`/`DownloadRequest`/etc.) crossing all seams.
- `internal/events/bus.go` — in-process EventBus backing `internal/api/stream.go` (WebSocket) and download progress; primary live channel to the frontend.
- `internal/matching` — matches search results against library (ISRC/metadata) to mark owned.
- `internal/resolver` — resolves adapter/track identity; constructed against `ServiceReloader.MatcherProvider()` so it reads the live matcher per-resolve.
- `internal/sync` — CRDT sync (HLC vector, per-field LWW, device pairing codes, Bearer token auth, Ed25519-signed changes, file manifests). Entity types and field names are the wire format and live in `sync.go`. `SyncQuerier` declares all persistence operations at compile time; pairing retains its separate interface. SQLite transaction selection and aggregate-value conversion remain in storage, with no mock-only changelog fallback. The change log only replicates facts; `internal/materialize` projects accepted changes onto the tables the app reads, installed via `SyncStore.SetMaterializer` in the composition root. It runs after the log commits and writes through the domain services without appending changes, so applying a peer's change cannot echo back.

  What replicates: catalog entities, per-track metadata (renames -> `track_override`, crops -> `track_crop`, quality -> `track_quality_override`, measured loudness -> `track_loudness`, uploaded art -> `entity_cover`), album and artist renames plus album art (`album`/`artist` entities -> `entity_override`, `entity_cover`), managed playlists, and play history -> `plays`.

  Album and artist changes cannot be keyed on a catalog id — there is no catalog entity behind them — so they travel under a **stable key** derived from the library's own names (`override.AlbumKey`, `override.ArtistKey`): the normalised primary artist plus title. A peer's change binds to whatever backend id this device has for that key, or waits under the key itself until one exists. Keys must always be derived from the library's original names, never from names an override has already rewritten, or the second rename of a thing keys differently from the first.

  A cover replicates as an address, not an image: the log carries `<sha256>.<ext>` and `p2p.Puller` fetches the bytes over `/reverb/cover/1.0.0` for any `entity_cover` row whose blob is missing. Until the bytes arrive the library backend's own art shows, so a missing blob is a delay, not a broken page.

  Everything per-track is keyed on the **catalog id**, never the backend track id, which is local to one library backend. Catalog ids are minted from a random token, so two devices mint different ids for the same track: `catalogEntity` changes replicate the entity itself, and `catalog.Adopt` fuses a peer's entity with a local one via the ordinary alias-collision merge. The peer's id keeps resolving afterwards through its `catalog` self-alias (`catalog.Resolve`), which merges repoint. Catalog entities are applied ahead of the rest of a batch, since everything else names a track by an id that means nothing until the entity has landed.

- `internal/metadata` — local track rename and crop commands own persistence and best-effort sync emission. Their `BackendID` input is resolved to a catalog ID before publication. Peer projection calls the underlying non-emitting override/crop operations; it never calls local commands. These edits retain their existing notification behavior (no new events are introduced).
- `internal/syncemit` — the one place that knows how to write to the log: device identity, catalog-entity publication (`EnsureCatalogEntity`), plays, per-track fields, plus the one-time `BackfillHistory` publish of state that predates replication.
- `internal/playlistcrdt` — managed playlists in both directions. Each track membership is its own field (`track:<digest>`) so concurrent additions on two devices both survive, and position is a fractional order key (`internal/fracidx`) so moving one track rewrites one key instead of renumbering the list. `Publish` diffs the playlist against the log rather than taking instructions, so every `playlistsync` mutator replicates through one call. Only `mode="once"` playlists replicate their tracklist; a `mode="synced"` mirror rebuilds itself from upstream on each device, so only its identity and settings travel.

  The member digest hashes `(source, externalID)` for search-source tracks, but a **library track's id belongs to one backend**, so those are keyed on `matching.Fingerprint` instead — otherwise the same recording added on two devices merges into two entries. `ExternalResult.CanonicalID` is stripped before publishing and re-applied from the local row on the way back in: it is device-local addressing, and carrying it would rewrite every member on every edit. A playlist row is only created once the log carries its `source`, since a peer sends its log in pages and half an entity must not fix the wrong identity onto a new row; fields the log does not carry are left as the row has them.
- `internal/cover` — user-uploaded album and track art. Blobs live under `<dataDir>/entity-covers`, addressed by the sha256 of their bytes, so one image applied to fifty albums is stored once. Nothing is written into the music library. An uploaded cover is surfaced by rewriting `CoverArtID` to `custom:<sha>.<ext>` on the way out of the API; `/cover/{id}` serves that from disk and everything else from the backend. `internal/api/decorate.go` is the single place library data passes through on its way out — renames, crops, and art are all applied there.
- `internal/p2p` — libp2p host (fixed listen port, `--p2p-port`), peer trust (`p2p_peer`), manifest/file sync handlers, cover-blob transfer, pull replication. Peers are dialed by stored multiaddr as well as by discovery, since mDNS multicast does not cross a VPN and the DHT runs in client mode; pairing accepts a full `/ip4/…/p2p/<id>` multiaddr and persists it on `p2p_peer.addrs`. A redeeming peer sends the device ID it already authors changes under and is bound to that (`PairingService.RedeemAs`), since the sync handler takes identity from the connection and refuses a round whose `deviceId` names anything else.
- `internal/store` — SQLite (`modernc.org/sqlite`), migrations `internal/store/migrations/*.sql` (goose), sqlc `internal/store/queries` -> `internal/store/db` (`make gen`).
- `internal/api` — chi handlers, OpenAPI at `/api/v1/openapi.yaml`, `embed.go` embeds SPA. Download transport records and frontend event validation are generated from OpenAPI; see [contracts.md](contracts.md).
- `internal/auth` + `internal/api/roles.go` — a single household owner (`local`, holding every capability) is the intended end state, not a stopgap. There are no accounts, no login and no sessions: the HTTP API is loopback-only (Reverb refuses a non-loopback bind without `--allow-network-access`) and the transport is the access boundary, so `requireAuth` fabricating the owner for every request is correct. Paired devices authenticate separately — Bearer tokens on `/sync`, libp2p peer identity on P2P. The capability gates (`can_manage_library`, etc.) are therefore never denials in practice; they document intent. Per-user columns from migration 0013 are inert by design.

  `csrfGuard` (`internal/api/security.go`) stays: with no session cookie it is tempting to think CSRF is impossible, but reaching the loopback port *is* the ambient credential, so a page in the user's browser could otherwise POST to Reverb as the owner. `hostGuard` sits beside it because the Origin check alone cannot see a DNS-rebinding page, which controls `Origin` and `Host` together and makes them agree; it rejects any request — read or write — whose `Host` is neither loopback nor in `Deps.AllowedHosts`, since a rebound page is same-origin to the browser and reads the responses it gets back. In the desktop build the window's own origin counts as local too: the SPA there is served by the same handler through the Wails asset server, so its requests arrive with `Host: wails` (`wails.localhost` on Windows) and a fabricated TEST-NET peer address rather than anything loopback — the same two hosts `ws.go` accepts as WebSocket origins, and the same reason `authenticateSync` treats them as local. Both guards exempt `POST /api/v1/sync` once its Bearer token has authenticated a paired device — no page can forge that shape without CORS — and nothing else.

### Frontend (`web/`)

React 19 + TypeScript, Vite, TanStack Query, Zustand, Tailwind, react-router.
- `web/src/routes/` — one file per page (Home, Library, Search, Album, Artist, Downloads, Requests, Settings, Admin, Stats, …) with co-located `*.test.tsx`.
- `web/src/lib/` — thin `*Api.ts` fetch wrappers, Zustand `*Store.ts` (player, download, coverage, auth, library revision, everywhere/search, now-playing, pending-play), `audioEngine.ts`, `mediaSession.ts`, `playTracker.ts`, `realtime.ts` (WebSocket), `paletteService.ts`/`paletteWorker.ts`.
- `web/src/components/` — shared UI.
- Dev: Go proxies Vite (`--dev`); prod: SPA embedded into binary (`-tags prod`).

### Desktop (`desktop/`)

Wails wrapper — same monolith on `127.0.0.1:0` (`desktop/main.go:boot`, `desktop/app.go:App`). Listens on random port, publishes `LocalAPIPort` so the AssetServer-served SPA dials the real API (WS cannot upgrade through Wails). DB at `~/Library/Application Support/Reverb/reverb.db` (macOS) / `~/.config/reverb/reverb.db` (Linux XDG) with legacy `./data/reverb.db` migration; downloads in `~/Music/Reverb` (`internal/desktop/paths.go`). Bundled `ffmpeg`/`navidrome`/`spotdl`/`yt-dlp`/`deno` resolved via `desktop/bundle.go:ResolveBundledTools` and injected before `config.Load` (`ApplyBundledToolEnv`); fetched into `desktop/tools/` via `make desktop-deps`. Build tags `desktop,production,webkit2_41` (`desktop/frontend.go` vs `desktop/run_fallback.go` plain HTTP). Single-instance lock in `desktop/singleinstance.go`.

Desktop close behaviour hands the backend to a headless `--background` process
after releasing the single-instance lock. It runs the same composition root at
reduced CPU priority, without Wails or a webview. Reopening requests shutdown via
a user-private Unix socket and waits for resource release before booting the UI.
The local `desktop.json` preference controls this handoff; an explicit quit or
update restart bypasses it. No login service is installed. See
[desktop background sync](../desktop/README.md#background-sync-macos-and-linux).

### Configuration

Flags > env > defaults. Flags: `--port`/`--bind`/`--p2p-port`/`--db`/`--dev`/`--update-repo`. Env: `REVERB_PORT`/`REVERB_BIND`/`REVERB_P2P_PORT`/`REVERB_DB`/`REVERB_DEV`/`REVERB_DOWNLOAD_DIR` / `REVERB_SPOTIFY_CLIENT_ID/SECRET` / `REVERB_LIBRARY_PASSWORD` / `REVERB_SPOTDL_PATH`/`REVERB_NAVIDROME_BIN`/`REVERB_YTDLP_PATH`/`REVERB_DENO_PATH` (+ navidrome listen/port vars) — see `README.md` + `internal/config`. Secrets via env/`.env` only (gitignored; `.env.example` template).

### Linting

`.golangci.yml`: errcheck, govet, ineffassign, misspell, staticcheck, unconvert. Deferred `Close()` ignores pre-configured. staticcheck disables QF1001/QF1003/ST1000/ST1003 (ST1003 because `CoverUrl` etc. is pervasive).
