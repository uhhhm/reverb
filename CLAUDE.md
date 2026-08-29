## What this is

This is a fork of Reverb, a self-hosted music app. It is a Go single-binary modular monolith with an
embedded React/TypeScript SPA. It unifies an existing music library (Subsonic/
Navidrome), online search (Deezer/Spotify), and one-click downloading (spotDL)
in one web UI. License is AGPL-3.0-only.

IMPORTANT: This is a fork, so published instructions and README are may be out of date. Keep this in mind when working and giving user advice. You should be security-conscious.

> **Desktop is primary.** The user runs only the Wails desktop app (`desktop/`). Treat it as the primary entry point — prefer `make desktop` / `make desktop-dev` and `desktop/tools/` bundled deps, build via `internal/app` (the shared composition root), and verify desktop paths/behavior. Docker/server (`cmd/reverb`, `docker-compose.yml`) is secondary/reference only.

## Commands

```bash
# Backend tests — NEVER use ./... ; web/node_modules contains vendored Go
go test ./cmd/... ./internal/...

# Frontend (from web/)
cd web && npm install
npm run test          # vitest unit/component tests
npm run e2e            # Playwright, hermetic/mocked
npm run lint            # eslint

# make targets
make gen     # regenerate sqlc code (queries -> Go) into internal/store/db
make web     # build the SPA, copy web/dist -> internal/api/dist
make build   # web + production binary (-tags prod) -> ./reverb
make desktop      # web + Wails desktop binary -> ./dist/reverb-desktop
make desktop-dev  # wails dev -projectdir ./desktop (hot reload via Vite :5173)
make desktop-deps # fetch ffmpeg/navidrome/deno + spotDL venv into desktop/tools/
make test    # backend tests + frontend unit tests
make clean   # remove build artifacts

# Run locally (two shells, hot reload) — server mode
cd web && npm run dev          # shell 1: Vite dev server
go run ./cmd/reverb --dev       # shell 2: Go server proxying Vite, prints URL (default :8090)

# Run a single Go test
go test ./internal/download/... -run TestName -v

# Run a single frontend test file
cd web && npx vitest run src/lib/downloadApi.test.ts
```

Go 1.23+, Node 22+. gofmt-clean is required (`gofmt -w` before committing).
Conventional Commits (`feat(scope): …`, `fix(scope): …`, `test(scope): …`, etc.).
TDD is the norm — git history shows RED-phase `test(...)` commits followed by
implementation commits; keep suites green.

## Architecture

Go modular monolith — single binary, React SPA embedded at build time (`-tags prod` via `internal/api/embed.go`; `--dev` proxies Vite). Both entry points (`cmd/reverb` server and `desktop/` Wails) share one composition root in `internal/app/build.go` (`Build` wires, `StartBackground` starts) so dependencies are wired once; entry points only own how they listen/shut down. Hot-reload of adapters happens live via `internal/app/reload.go` (`ServiceReloader`) with no restart.

### Adapter/seam pattern (the core design)

Three pluggable seams, same shape: interface + adapters + conformance suite + explicit registry, **no `init()` side-effects**.

- **`library`** — `internal/library/library.go` (`LibraryAdapter`); adapters `internal/library/subsonic` + `internal/library/embedded` (built-in vs external, see below); `conformance.go`. Library data is never persisted by Reverb, solely proxied.
- **`search`** — `internal/search/search.go` (`SearchSource`); adapters `internal/search/deezer` (keyless), `internal/search/spotify`; `aggregator.go` fans out to all enabled sources concurrently over SSE with per-source `Envelope{Status,Results,Error}`; `conformance.go`. Optional caps via type assertion: `DiscographyProvider`, `TrackProvider`, `PlaylistProvider`, `PlaylistSearchProvider`.
- **`downloader`** — `internal/download/download.go` (`Downloader` + `DownloaderEntry{Order}` for per-instance granularity); adapters `internal/download/spotdl`, `internal/download/lidarr`, `internal/download/ytdlp`; `conformance.go`; `download/manager.go` owns queue/workers/dedup-join/fallback/scan-debounce/cancel/retry. Optional caps via type assertion: `AsyncDownloader` (Submit/Poll, reconciler lane) and `ChapterLister`.

Registration: `internal/registry/registry.go` holds constructors by name; `internal/app/build.go` registers `subsonic` / `spotify`+`deezer` / `spotdl`+`lidarr`+`ytdlp` explicitly, plus `registry.RegisterCapability("async", ...)`. `internal/wiring/wiring.go` (`Builder`) builds the active `ServiceBundle` from enabled `adapter_instance` DB rows; `internal/app/reload.go` rebuilds it on API mutations (`internal/api` adapter handlers) and publishes the new matcher/aggregator via holder so `resolver` and `extstream` see it live.

Library modes (`internal/library/embedded`): **built-in** bundles Navidrome as a supervised child process against the same music dir (waveform-peaks via local file access); **external** points at a user-provided Subsonic/Navidrome. Mode is boot-bound (restart to change).

### Composition & control flow

- `internal/app/build.go` — composes `auth`, `catalog`, `resolver`, `download.Manager`, `playlistsync`, `scrobble`, `extstream`, `api.Deps`, `sync`, `p2p`; entry points call `Build` then `StartBackground` (supervisor, manager, backfill, sync scheduler, scrobble worker, p2p host + sync/file handlers).
- `internal/core` — domain types (`Artist`/`Album`/`ExternalResult`/`DownloadRequest`/etc.) crossing all seams.
- `internal/events/bus.go` — in-process EventBus backing `internal/api/stream.go` (WebSocket) and download progress; primary live channel to the frontend.
- `internal/matching` — matches search results against library (ISRC/metadata) to mark owned.
- `internal/resolver` — resolves adapter/track identity; constructed against `ServiceReloader.MatcherProvider()` so it reads the live matcher per-resolve.
- `internal/sync` — CRDT sync (HLC vector, per-field LWW, device pairing codes, Bearer token auth, Ed25519-signed changes, file manifests).
- `internal/p2p` — libp2p host (fixed listen port, `--p2p-port`), peer trust (`p2p_peer`), manifest/file sync handlers, pull replication. Peers are dialed by stored multiaddr as well as by discovery, since mDNS multicast does not cross a VPN and the DHT runs in client mode; pairing accepts a full `/ip4/…/p2p/<id>` multiaddr and persists it on `p2p_peer.addrs`.
- `internal/store` — SQLite (`modernc.org/sqlite`), migrations `internal/store/migrations/*.sql` (goose), sqlc `internal/store/queries` -> `internal/store/db` (`make gen`).
- `internal/api` — chi handlers, OpenAPI at `/api/v1/openapi.yaml` (keep in sync), `embed.go` embeds SPA.
- `internal/auth` + `internal/api/roles.go` — a single household owner (`local`, holding every capability) is the intended end state, not a stopgap. There are no accounts, no login and no sessions: the HTTP API is loopback-only (Reverb refuses a non-loopback bind without `--allow-network-access`) and the transport is the access boundary, so `requireAuth` fabricating the owner for every request is correct. Paired devices authenticate separately — Bearer tokens on `/sync`, libp2p peer identity on P2P. The capability gates (`can_manage_library`, etc.) are therefore never denials in practice; they document intent. Per-user columns from migration 0013 are inert by design.

  `csrfGuard` (`internal/api/security.go`) stays: with no session cookie it is tempting to think CSRF is impossible, but reaching the loopback port *is* the ambient credential, so a page in the user's browser could otherwise POST to Reverb as the owner. The Origin check is the only thing stopping it.

### Frontend (`web/`)

React 19 + TypeScript, Vite, TanStack Query, Zustand, Tailwind, react-router.
- `web/src/routes/` — one file per page (Home, Library, Search, Album, Artist, Downloads, Requests, Settings, Admin, Stats, …) with co-located `*.test.tsx`.
- `web/src/lib/` — thin `*Api.ts` fetch wrappers, Zustand `*Store.ts` (player, download, coverage, auth, library revision, everywhere/search, now-playing, pending-play), `audioEngine.ts`, `mediaSession.ts`, `playTracker.ts`, `realtime.ts` (WebSocket), `paletteService.ts`/`paletteWorker.ts`.
- `web/src/components/` — shared UI.
- Dev: Go proxies Vite (`--dev`); prod: SPA embedded into binary (`-tags prod`).

### Desktop (`desktop/`)

Wails wrapper — same monolith on `127.0.0.1:0` (`desktop/main.go:boot`, `desktop/app.go:App`). Listens on random port, publishes `LocalAPIPort` so the AssetServer-served SPA dials the real API (WS cannot upgrade through Wails). DB at `~/Library/Application Support/Reverb/reverb.db` (macOS) / `~/.config/reverb/reverb.db` (Linux XDG) with legacy `./data/reverb.db` migration; downloads in `~/Music/Reverb` (`internal/desktop/paths.go`). Bundled `ffmpeg`/`navidrome`/`spotdl`/`yt-dlp`/`deno` resolved via `desktop/bundle.go:ResolveBundledTools` and injected before `config.Load` (`ApplyBundledToolEnv`); fetched into `desktop/tools/` via `make desktop-deps`. Build tags `desktop,production,webkit2_41` (`desktop/frontend.go` vs `desktop/run_fallback.go` plain HTTP). Single-instance lock in `desktop/singleinstance.go`.

### Configuration

Flags > env > defaults. Flags: `--port`/`--bind`/`--p2p-port`/`--db`/`--dev`/`--update-repo`. Env: `REVERB_PORT`/`REVERB_BIND`/`REVERB_P2P_PORT`/`REVERB_DB`/`REVERB_DEV`/`REVERB_DOWNLOAD_DIR`/`REVERB_ADMIN_PASSWORD` (first-run) / `REVERB_SPOTIFY_CLIENT_ID/SECRET` / `REVERB_LIBRARY_PASSWORD` / `REVERB_SPOTDL_PATH`/`REVERB_NAVIDROME_BIN`/`REVERB_YTDLP_PATH`/`REVERB_DENO_PATH` (+ navidrome listen/port vars) — see `README.md` + `internal/config`. Secrets via env/`.env` only (gitignored; `.env.example` template).

### Linting

`.golangci.yml`: errcheck, govet, ineffassign, misspell, staticcheck, unconvert. Deferred `Close()` ignores pre-configured. staticcheck disables QF1001/QF1003/ST1000/ST1003 (ST1003 because `CoverUrl` etc. is pervasive).

## Project rules

**Write the current truth, not its history.** Do not layer a correction on top of
a claim you are correcting — delete the claim and state what is true now.

Likewise for decisions: a section headed "the open decision" followed by
"resolved" followed by "superseded" is archaeology. Record what is being done and
why. Delete the rest.

Do not pad documentation and docstrings with filler sections, redundant summaries, or boilerplate.

**Responding:** Keep responses focused, brief, and concise. Keep disclaimers and caveats short, and spend most of the response on the main answer.

## Developing

- When asked to commit, commit on main branch. Do not create separate branches.
- Do not use claude browser to preview/test — the user does this themselves.
- Use ripgrep instead of grep — its much faster.
